package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/dgrijalva/jwt-go"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/hashicorp/go-multierror"
	"github.com/julienschmidt/httprouter"
	"google.golang.org/api/cloudiot/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"nhooyr.io/websocket"
)

var (
	cloudiotAPIClient cloudiotAPI

	subMan *subscriptionManager
)

type subscriptionMessage struct {
	Topic string `json:"topic"`
	Data  string `json:"data"`
}

type subscriptionCallback = func(*subscriptionMessage)

type iotCoreAPI interface {
	io.Closer
	SendCommand(ctx context.Context, simulation, deviceID, subfolder string, message interface{}) error
	Subscribe(ctx context.Context, simulation, deviceID, subfolder string, callback subscriptionCallback) error
}

type cloudiotAPI struct {
	iotClient *cloudiot.Service
	mutex     sync.RWMutex
}

func (p *cloudiotAPI) getIOTClient(ctx context.Context) (*cloudiot.Service, error) {
	p.mutex.RLock()
	if p.iotClient == nil {
		p.mutex.RUnlock()
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if p.iotClient == nil {
			var err error
			p.iotClient, err = cloudiot.NewService(ctx)
			if err != nil {
				return nil, err
			}
		}
	} else {
		defer p.mutex.RUnlock()
	}
	return p.iotClient, nil
}

func parsePublicKey(
	rawkey *cloudiot.PublicKeyCredential,
) (key interface{}, alg string, err error) {
	switch rawkey.Format {
	case "RSA_X509_PEM":
		key, err := jwt.ParseRSAPublicKeyFromPEM([]byte(rawkey.Key))
		return key, "RS256", err
	case "ES256_X509_PEM":
		key, err := jwt.ParseECPublicKeyFromPEM([]byte(rawkey.Key))
		return key, "ES256", err
	default:
		return nil, "", errors.New("unsupported format: " + rawkey.Format)
	}
}

func validateDeviceCredential(
	cred *cloudiot.DeviceCredential,
	keyAlgorithm string,
) (pubKey interface{}, err error) {
	expires, err := time.Parse(time.RFC3339, cred.ExpirationTime)
	if err != nil {
		return nil, errors.New("expiry time is invalid: " + cred.ExpirationTime)
	}
	// A non-expiring credential has an expiry time equal to Unix zero time.
	if expires.Unix() != 0 && time.Now().After(expires) {
		return nil, errors.New("expired at " + expires.String())
	}
	pubKey, alg, err := parsePublicKey(cred.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}
	if alg != keyAlgorithm {
		return nil, nil
	}
	return pubKey, nil
}

type deviceIDJWTClaim struct {
	jwt.StandardClaims
	DeviceID string `json:"device_id"`
}

func (p *cloudiotAPI) GetDeviceCredentials(ctx context.Context, deviceID, alg string) (interface{}, error) {
	iot, err := p.getIOTClient(ctx)
	if err != nil {
		return nil, err
	}
	device, err := iot.Projects.Locations.Registries.Devices.Get(
		fmt.Sprintf(
			"projects/%s/locations/%s/registries/%s/devices/%s",
			projectID, region, registryID, deviceID,
		),
	).Context(ctx).FieldMask("credentials").Do()
	if err != nil {
		return nil, err
	}
	for _, cred := range device.Credentials {
		key, err := validateDeviceCredential(cred, alg)
		if key != nil {
			return key, nil
		} else if err != nil {
			log.Printf("error while validating credential for device '%s': %v", deviceID, err)
		}
	}
	return nil, fmt.Errorf("device '%s has no key with algorithm %s", deviceID, alg)
}

func newDroneTokenHeader(ctx context.Context, simulation, deviceID string) (http.Header, error) {
	secret, err := getKube().CoreV1().Secrets(simulation).Get(ctx, "drone-"+deviceID+"-secret", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	signedToken, err := signDroneJWT(deviceID, secret.Data["DRONE_IDENTITY_KEY"])
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+signedToken)
	return header, nil
}

func (p *cloudiotAPI) Close() error {
	return nil
}

func (p *cloudiotAPI) SendCommand(ctx context.Context, simulation, deviceID, subfolder string, message interface{}) error {
	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if !cloudMode {
		header, err := newDroneTokenHeader(ctx, simulation, deviceID)
		if err != nil {
			return err
		}
		return postJSON(ctx, cloudSimulationCoordinatorURL+"/commands", header, nil, obj{
			"subfolder": subfolder,
			"message":   string(msg),
		})
	}
	req := cloudiot.SendCommandToDeviceRequest{
		BinaryData: base64.StdEncoding.EncodeToString(msg),
		Subfolder:  subfolder,
	}
	name := fmt.Sprintf(
		"projects/%s/locations/%s/registries/%s/devices/%s",
		projectID, region, registryID, deviceID,
	)
	iot, err := p.getIOTClient(ctx)
	if err != nil {
		return err
	}
	_, err = iot.Projects.Locations.Registries.Devices.
		SendCommandToDevice(name, &req).Context(ctx).Do()
	return err
}

func (p *cloudiotAPI) Subscribe(ctx context.Context, simulation, deviceID, subfolder string, callback subscriptionCallback) error {
	if cloudMode {
		return subMan.receive(ctx, deviceID, subfolder, callback)
	}
	header, err := newDroneTokenHeader(ctx, simulation, deviceID)
	if err != nil {
		return err
	}
	conn, err := connectWebSocket(
		ctx,
		cloudSimulationCoordinatorURL+"/events/"+deviceID+subfolder,
		header,
	)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusGoingAway, "")
	for {
		var msg subscriptionMessage
		if err = conn.ReadJSON(ctx, &msg); err != nil {
			return err
		}
		callback(&msg)
	}
}

type subscriptionManager struct {
	client        *pubsub.Client
	subscriptions sync.Map
}

func newSubscriptionManager(ctx context.Context) (m *subscriptionManager, err error) {
	m = &subscriptionManager{}
	m.client, err = pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *subscriptionManager) subscribeToPubsub(ctx context.Context, subs []string) error {
	errs := make(chan error, len(subs))
	count := 0
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		count++
		sub := s.client.Subscription(sub)
		go func() {
			errs <- sub.Receive(ctx, func(c context.Context, m *pubsub.Message) {
				defer m.Ack()
				msgDeviceID, ok := m.Attributes["deviceId"]
				if !ok {
					log.Println("pubsub message is missing deviceId")
					return
				}
				msgSubFolder, ok := m.Attributes["subFolder"]
				if !ok {
					log.Println("pubsub message is missing subFolder")
					return
				}
				if msgSubFolder == "" || msgSubFolder[0] != '/' {
					msgSubFolder = "/" + msgSubFolder
				}
				if msgSubFolder[len(msgSubFolder)-1] != '/' {
					msgSubFolder = msgSubFolder + "/"
				}
				if set, ok := s.subscriptions.Load(msgDeviceID); ok {
					set.(*subscriptionSet).notify(&subscriptionMessage{
						Topic: msgSubFolder,
						Data:  string(m.Data),
					})
				}
			})
		}()
	}
	if count == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	var err *multierror.Error
	for i := 0; i < len(subs); i++ {
		err = multierror.Append(err, <-errs)
	}
	return err.ErrorOrNil()
}

func (m *subscriptionManager) receive(ctx context.Context, deviceID, subfolder string, callback subscriptionCallback) error {
	set, _ := m.subscriptions.LoadOrStore(deviceID, &subscriptionSet{})
	msgs, remove := set.(*subscriptionSet).add()
	defer remove()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-msgs:
			if strings.HasPrefix(msg.Topic, subfolder) {
				callback(msg)
			}
		}
	}
}

type subscriptionSet struct {
	channels sync.Map
}

func (l *subscriptionSet) add() (<-chan *subscriptionMessage, func()) {
	c := make(chan *subscriptionMessage)
	l.channels.Store(&c, c)
	return c, func() { l.channels.Delete(&c) }
}

func (l *subscriptionSet) notify(msg *subscriptionMessage) {
	l.channels.Range(func(_, value interface{}) bool {
		value.(chan *subscriptionMessage) <- msg
		return true
	})
}

type mqttPublisher struct {
	client mqtt.Client
}

func (p *mqttPublisher) Close() error {
	p.client.Disconnect(1000)
	return nil
}

func (p *mqttPublisher) SendCommand(ctx context.Context, simulation, deviceID, subfolder string, message interface{}) error {
	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}
	pubtok := p.client.Publish(fmt.Sprintf("/devices/%s/commands/%s", deviceID, subfolder), 1, false, msg)
	if !pubtok.WaitTimeout(2 * time.Second) {
		return errors.New("publish timeout")
	}
	if err := pubtok.Error(); err != nil {
		return fmt.Errorf("could not publish command: %w", err)
	}
	return nil
}

func (p *mqttPublisher) Subscribe(ctx context.Context, simulation, deviceID, subfolder string, callback subscriptionCallback) error {
	prefix := fmt.Sprintf("/devices/%s/events", deviceID)
	topics := prefix + subfolder + "#"
	token := p.client.Subscribe(topics, 0, func(client mqtt.Client, msg mqtt.Message) {
		callback(&subscriptionMessage{
			Topic: strings.TrimPrefix(msg.Topic(), prefix),
			Data:  string(msg.Payload()),
		})
	})
	defer p.client.Unsubscribe(topics)
	if err := token.Error(); err != nil {
		return fmt.Errorf("could not subscribe: %w", err)
	}
	<-ctx.Done()
	return ctx.Err()
}

func getIotCoreClient(ctx context.Context, server string) (p iotCoreAPI, err error) {
	if server == mqttServerURL {
		return &cloudiotAPIClient, nil
	}
	opts := mqtt.NewClientOptions().
		AddBroker(server).
		SetUsername("dronsole").
		SetPassword("").
		SetProtocolVersion(4) // Use MQTT 3.1.1
	client := mqtt.NewClient(opts)
	tok := client.Connect()
	if !tok.WaitTimeout(time.Second * 5) {
		return nil, errors.New("MQTT connection timeout")
	}
	if err := tok.Error(); err != nil {
		return nil, fmt.Errorf("could not connect to MQTT: %w", err)
	}
	return &mqttPublisher{client: client}, nil
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	if subMan == nil {
		writeError(w, "event stream is not enabled", nil, http.StatusNotImplemented)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(ctx)
	droneID := params.ByName("droneID")
	path := params.ByName("path")

	allowedDeviceID, err := validateDeviceAuthHeader(r)
	if err != nil {
		writeUnauthorized(w, "nonexistent or invalid JWT", err)
		return
	}
	if allowedDeviceID != droneID {
		writeError(w, "only device "+allowedDeviceID+" is allowed", nil, http.StatusForbidden)
		return
	}

	conn, err := acceptWebsocket(w, r)
	if err != nil {
		writeBadRequest(w, "failed to upgrade connection", err)
		return
	}
	defer conn.Close(websocket.StatusGoingAway, "")
	subMan.receive(ctx, droneID, path, func(msg *subscriptionMessage) {
		if err := conn.WriteJSON(ctx, msg); err != nil {
			log.Println(err)
			cancel()
		}
	})
}

func signDroneJWT(deviceID string, privateKey []byte) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey)
	if err != nil {
		return "", err
	}
	t := time.Now()
	return jwt.NewWithClaims(jwt.GetSigningMethod("RS256"), &deviceIDJWTClaim{
		StandardClaims: jwt.StandardClaims{
			IssuedAt:  t.Unix(),
			ExpiresAt: t.Add(24 * time.Hour).Unix(),
			Audience:  projectID,
		},
		DeviceID: deviceID,
	}).SignedString(key)
}

func validateDeviceAuthHeader(r *http.Request) (string, error) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var deviceID string
	_, err := jwt.ParseWithClaims(token, &deviceIDJWTClaim{}, func(t *jwt.Token) (interface{}, error) {
		deviceID = t.Claims.(*deviceIDJWTClaim).DeviceID
		return cloudiotAPIClient.GetDeviceCredentials(r.Context(), deviceID, t.Method.Alg())
	})
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

type jsonString string

func (s jsonString) MarshalJSON() ([]byte, error) {
	return []byte(s), nil
}

func sendCommandHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subfolder string `json:"subfolder"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "body must be valid JSON", err)
		return
	}
	deviceID, err := validateDeviceAuthHeader(r)
	if err != nil {
		writeUnauthorized(w, "nonexistent or invalid JWT", err)
		return
	}
	switch req.Subfolder {
	case "videostream", "control":
	default:
		writeBadRequest(w, "subfolder '"+req.Subfolder+"' is not allowed", nil)
		return
	}
	err = cloudiotAPIClient.SendCommand(
		r.Context(),
		"",
		deviceID,
		req.Subfolder,
		jsonString(req.Message),
	)
	if err != nil {
		writeServerError(w, "failed to send command", err)
		return
	}
	writeJSON(w, obj{})
}
