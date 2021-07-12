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
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-multierror"
	"github.com/julienschmidt/httprouter"
	"google.golang.org/api/cloudiot/v1"
)

var (
	cloudiotAPIClient      cloudiotAPI
	cloudiotAPIClientMutex sync.RWMutex

	subMan *subscriptionManager
)

type subscriptionMessage struct {
	Topic string `json:"topic"`
	Data  string `json:"data"`
}

type subscriptionCallback = func(*subscriptionMessage)

type iotCoreAPI interface {
	io.Closer
	SendCommand(ctx context.Context, deviceID, subfolder string, message interface{}) error
	Subscribe(ctx context.Context, deviceID, subfolder string, callback subscriptionCallback) error
}

type cloudiotAPI struct {
	iotClient *cloudiot.Service
}

func (p *cloudiotAPI) Close() error {
	return nil
}

func (p *cloudiotAPI) SendCommand(ctx context.Context, deviceID, subfolder string, message interface{}) error {
	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}
	req := cloudiot.SendCommandToDeviceRequest{
		BinaryData: base64.StdEncoding.EncodeToString(msg),
		Subfolder:  subfolder,
	}
	name := fmt.Sprintf(
		"projects/%s/locations/%s/registries/%s/devices/%s",
		projectID, region, registryID, deviceID,
	)
	_, err = p.iotClient.Projects.Locations.Registries.Devices.
		SendCommandToDevice(name, &req).Context(ctx).Do()
	return err
}

func (p *cloudiotAPI) Subscribe(ctx context.Context, deviceID, subfolder string, callback subscriptionCallback) error {
	if enableEventPubsub {
		return subMan.receive(ctx, deviceID, subfolder, callback)
	}
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, eventsAPIWSURL+"/events/"+deviceID+subfolder, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		defer conn.Close()
		<-ctx.Done()
		conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(5*time.Second))
	}()
	for {
		var msg subscriptionMessage
		if err = conn.ReadJSON(&msg); err != nil {
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

func (p *mqttPublisher) SendCommand(ctx context.Context, deviceID, subfolder string, message interface{}) error {
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

func (p *mqttPublisher) Subscribe(ctx context.Context, deviceID, subfolder string, callback subscriptionCallback) error {
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
		cloudiotAPIClientMutex.RLock()
		if cloudiotAPIClient.iotClient == nil {
			cloudiotAPIClientMutex.RUnlock()
			cloudiotAPIClientMutex.Lock()
			defer cloudiotAPIClientMutex.Unlock()
			if cloudiotAPIClient.iotClient == nil {
				cloudiotAPIClient.iotClient, err = cloudiot.NewService(context.Background())
				if err != nil {
					return nil, err
				}
			}
		} else {
			defer cloudiotAPIClientMutex.RUnlock()
		}
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

	conn, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		writeBadRequest(w, "failed to upgrade connection", err)
		return
	}
	defer conn.Close()

	subMan.receive(ctx, droneID, path, func(msg *subscriptionMessage) {
		if err := conn.WriteJSON(msg); err != nil {
			if !websocket.IsCloseError(
				err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
			) {
				log.Println(err)
			}
			cancel()
		}
	})
}
