package main

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/julienschmidt/httprouter"
	"github.com/tiiuae/fleet-management/simulation-coordinator/kube"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"nhooyr.io/websocket"
)

var client *kube.Client

const minDroneRecordSizeThreshold = 100_000

const simulationAdminRole = "simulation-admin"

// GET /simulations
func getSimulationsHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	claims, err := getClaimsFromRequest(r)
	if err != nil {
		writeUnauthorized(w, "invalid auth token", err)
		return
	}
	sims, err := client.GetSimulations(c)
	if err != nil {
		writeServerError(w, "Could not get simulations", err)
		return
	}
	var response []kube.Simulation
	for _, sim := range sims {
		if claims.hasAccess(sim.Owners) {
			response = append(response, sim)
		}
	}
	writeJSON(w, obj{"simulations": response})
}

func getSimulationHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")

	simType, err := client.GetSimulationType(r.Context(), simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	if simType == kube.SimulationGlobal {
		writeJSON(w, obj{"mqtt_server": obj{"url": mqttServerURL}})
		return
	}
	hp, err := waitHostPort(r.Context(), simulationName, "mqtt-server-public-svc")
	if err != nil {
		writeServerError(w, "Error getting mqtt server address", err)
		return
	}
	writeJSON(w, obj{"mqtt_server": obj{"url": "tcp://" + hp}})
}

// POST /simulations
func createSimulationHandler(w http.ResponseWriter, r *http.Request) {
	claims, err := getClaimsFromRequest(r)
	if err != nil {
		writeUnauthorized(w, "invalid Authorization header", err)
		return
	}
	c := r.Context()
	var request struct {
		Name                 string `json:"name"`
		World                string `json:"world"`
		Standalone           bool   `json:"standalone"`
		DataImage            string `json:"data_image"`
		MissionDataDirectory string `json:"mission_data_directory"`
		GPUMode              string `json:"gpu_mode"`
	}
	err = json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeBadRequest(w, "Could not unmarshal simulation request", err)
		return
	}
	if storeStandaloneMissionDataLocally && request.Standalone && request.MissionDataDirectory == "" {
		writeBadRequest(w, "mission_data_directory must not be empty for standalone simulations", nil)
		return
	}
	gpuMode := defaultSimulationGPUMode
	if request.GPUMode != "" {
		if gpuMode.Set(request.GPUMode); err != nil {
			writeBadRequest(w, "gpu_mode is invalid", err)
			return
		}
	}

	if request.Name == "" {
		request.Name = generateSimulationName(c)
	}
	simulationID := generateSimulationID(request.Name)

	log.Printf("Creating simulation %s with world %s", request.Name, request.World)

	simType := kube.SimulationGlobal
	if request.Standalone {
		simType = kube.SimulationStandalone
	}
	opts := kube.CreateNamespaceOptions{
		Name:           request.Name,
		ID:             simulationID,
		SimType:        simType,
		ExpiryDuration: defaultExpiryDuration,
		Owners:         []string{claims.Subject},
	}
	ns, err := client.CreateNamespace(c, &opts)
	if err != nil {
		writeError(w, "Could not create namespace for the simulation", err, http.StatusInternalServerError)
		return
	}
	creationSucceeded := false
	var creationError error
	defer func() {
		if !creationSucceeded {
			writeServerError(w, "failed to create simulation", creationError)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err := client.Clientset.CoreV1().Namespaces().Delete(ctx, request.Name, *metav1.NewDeleteOptions(10))
			if err != nil {
				log.Println("Unable to delete namespace:", err)
			}
		}
	}()
	err = client.CopySecret(c, currentNamespace, dockerConfigSecretName, ns.Name, "dockerconfigjson")
	if err != nil {
		creationError = fmt.Errorf("Could not copy Docker configuration for the simulation: %w", err)
		return
	}

	err = client.CreateGZServer(c, request.Name, imageGZServer, request.DataImage, gpuMode, cloudMode)
	if err != nil {
		creationError = fmt.Errorf("failed to create gzserver: %w", err)
		return
	}

	if request.Standalone {
		err = client.CreateMQTT(c, request.Name, imageMQTTServer, !cloudMode)
		if err != nil {
			creationError = fmt.Errorf("error creating mqtt-server deployment: %w", err)
			return
		}
		err = client.CreateMissionControl(c, request.Name, imageMissionControl)
		if err != nil {
			creationError = fmt.Errorf("error creating mission-control deployment: %w", err)
			return
		}
		videoKey, videoCert, err := generateCertificate()
		if err != nil {
			creationError = fmt.Errorf("error generating video-server certificates: %w", err)
			return
		}
		err = client.CreateVideoServer(c, request.Name, imageVideoServer, videoCert, videoKey)
		if err != nil {
			creationError = fmt.Errorf("error creating video-server deployment: %w", err)
			return
		}
		err = client.CreateVideoStreamer(c, request.Name, imageVideoStreamer, "/simulations/"+request.Name+"/video/")
		if err != nil {
			creationError = fmt.Errorf("error creating video-streamer deployment: %w", err)
			return
		}
		err = client.CreateWebBackend(c, request.Name, imageWebBackend)
		if err != nil {
			creationError = fmt.Errorf("error creating web-backend deployment: %w", err)
			return
		}
		opts := &kube.MissionDataRecorderBackendOptions{
			Namespace: request.Name,
			Image:     imageMissionDataRecorderBackend,
		}
		if storeStandaloneMissionDataLocally {
			opts.DataDirectory = filepath.Join(request.MissionDataDirectory, request.Name)
		} else {
			opts.Cloud = &kube.MissionDataRecorderBackendCloudOptions{
				ProjectID:        projectID,
				RegistryID:       registryID,
				Region:           region,
				Bucket:           standaloneMissionDataBucket,
				JSONKey:          missionDataRecorderBackendKey,
				DataObjectPrefix: simulationID,
			}
		}
		err = client.CreateMissionDataRecorderBackend(c, opts)
		if err != nil {
			creationError = fmt.Errorf("error creating mission-data-recorder-backend: %w", err)
			return
		}
	}

	// request world creation
	requestBody, err := json.Marshal(obj{
		"world_file": request.World,
	})
	if err != nil {
		creationError = errors.New("Could not marshal body")
		return
	}

	if err = waitDeploymentAvailable(c, request.Name, "gzserver-dep"); err != nil {
		creationError = fmt.Errorf("failed to wait for gzserver: %w", err)
		return
	}

	// start the simulation by calling the service
	startURL := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/start", request.Name)
	// retry max 32 times
	for i := 0; i < 32; i++ {
		_, err = http.Post(startURL, "application/json", bytes.NewBuffer(requestBody))
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		creationError = fmt.Errorf("Could not start simulation on gzserver: %w", err)
		return
	}
	log.Printf("Simulation started")
	writeJSON(w, obj{"name": request.Name})
	creationSucceeded = true
}

func removeSimulationHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")

	err := client.RemoveSimulation(c, simulationName)
	if errors.Is(err, kube.ErrSimulationDoesntExist) {
		writeNotFound(w, "Simulation doesn't exist", nil)
	} else if err != nil {
		writeServerError(w, "Could not delete simulation", err)
	}
}

var viewerClients sync.Map // map[string]nil

func startViewerHandler(w http.ResponseWriter, r *http.Request) {
	c, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	simCoordSvc, err := client.Clientset.CoreV1().Services(currentNamespace).Get(c, "simulation-coordinator-svc", metav1.GetOptions{})
	if err != nil {
		writeServerError(w, "failed to get simulation coordinator port", err)
		return
	}
	err = client.CreateViewer(
		c,
		simulationName,
		imageGZWeb,
		fmt.Sprint(
			"http://simulation-coordinator-svc.",
			currentNamespace,
			":",
			simCoordSvc.Spec.Ports[0].Port,
		),
	)
	if err != nil {
		writeServerError(w, "Unable to create viewer", err)
		return
	}
	var viewerClientID string
	for {
		viewerClientID = uuid.New().String()
		_, loaded := viewerClients.LoadOrStore(viewerClientID, nil)
		if !loaded {
			defer viewerClients.Delete(viewerClientID)
			break
		}
	}
	conn, err := acceptWebsocket(w, r)
	if err != nil {
		writeBadRequest(w, "failed to start websocket connection", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	hp, err := waitHostPort(c, simulationName, "gzweb-svc")
	if err != nil {
		writeServerError(w, "failed to get IP", err)
		return
	}
	resp := obj{"id": viewerClientID, "host": hp}
	if err = conn.WriteJSON(c, resp); err != nil {
		log.Println(err)
		return
	}
	<-conn.CloseRead(c).Done()
	log.Println("viewer client closed")
}

func validateViewerClientID(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	if _, ok := viewerClients.Load(params.ByName("viewerClientID")); !ok {
		writeError(w, "forbidden", nil, http.StatusForbidden)
	}
}

func addDroneHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	var request struct {
		DroneID  string  `json:"drone_id"`
		PosX     float64 `json:"pos_x"`
		PosY     float64 `json:"pos_y"`
		PosZ     float64 `json:"pos_z"`
		Yaw      float64 `json:"yaw"`
		Pitch    float64 `json:"pitch"`
		Roll     float64 `json:"roll"`
		Location string  `json:"location"` // "cluster", "local" or "remote"

		// the following are ignored if Location != "cluster"
		PrivateKey          string   `json:"private_key"`
		RecordTopics        []string `json:"record_topics"`
		RecordSizeThreshold int      `json:"record_size_threshold"`

		// the following are ignored if Location == "cluster"
		MAVLinkAddress string `json:"mavlink_address"`
		MAVLinkUDPPort int    `json:"mavlink_udp_port"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Could not unmarshal simulation request", err, http.StatusInternalServerError)
		return
	}
	creationSucceeded := false
	defer func() {
		if !creationSucceeded {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err := client.DeleteDrone(ctx, simulationName, request.DroneID)
			if err != nil {
				log.Println("Unable to delete drone:", err)
			}
		}
	}()
	switch request.Location {
	case "cluster":
		simType, err := client.GetSimulationType(c, simulationName)
		if errors.Is(err, kube.ErrSimulationDoesntExist) {
			writeNotFound(w, "simulation doesn't exist", nil)
			return
		} else if err != nil {
			writeServerError(w, "failed to add drone", err)
			return
		}
		if request.RecordSizeThreshold < minDroneRecordSizeThreshold {
			writeBadRequest(w, "record_size_threshold must be at least "+strconv.Itoa(minDroneRecordSizeThreshold), nil)
			return
		}
		opts := &kube.CreateDroneOptions{
			Image:     imageFogDrone,
			Namespace: simulationName,
			MissionDataRecording: kube.MissionDataRecordingOptions{
				SizeThreshold: request.RecordSizeThreshold,
				Topics:        request.RecordTopics,
			},
		}
		switch simType {
		case kube.SimulationGlobal:
			if request.PrivateKey == "" {
				writeBadRequest(w, "identity key is required for non-standalone simulations", nil)
				return
			}
			if request.DroneID == "" {
				writeBadRequest(w, "drone ID is required for non-standalone simulations", nil)
				return
			}
			opts.MQTTBrokerAddress = mqttServerURL
			opts.RTSPServerAddress = urlWithAuth(*videoServerURL.URL)
			opts.MissionDataRecording.BackendURL = missionDataRecorederBackendCloudURL
		case kube.SimulationStandalone:
			if request.PrivateKey == "" {
				request.PrivateKey, _, err = generateCertificate()
				if err != nil {
					writeBadRequest(w, "Automatic generation of private key failed. Provide it in the request body.", nil)
					return
				}
			}
			if request.DroneID == "" {
				request.DroneID, err = generateDroneID(c, simulationName)
				if err != nil {
					writeBadRequest(w, "Automatic generation of drone ID failed. Provide a unique ID in the request body.", err)
					return
				}
			}
			opts.MQTTBrokerAddress = "tcp://mqtt-server-svc:8883"
			videoSvc, err := client.Clientset.CoreV1().Services(simulationName).Get(c, "video-server-svc", metav1.GetOptions{})
			if err != nil {
				writeServerError(w, "failed to get video server port", err)
				return
			}
			opts.RTSPServerAddress = fmt.Sprint(
				"rtsp://",
				videoServerUsername,
				":",
				videoServerPassword,
				"@video-server-svc:",
				videoSvc.Spec.Ports[0].Port,
			)
			opts.MissionDataRecording.BackendURL = "http://mission-data-recorder-backend-svc"
		default:
			panic("invalid simulation type: " + simType)
		}
		opts.DeviceID = request.DroneID
		opts.PrivateKey = request.PrivateKey
		err = client.CreateDrone(c, opts)
		if err != nil {
			if errors.Is(err, kube.ErrDroneExists) {
				creationSucceeded = true
			}
			writeServerError(w, "failed to add drone", err)
			return
		}
		request.MAVLinkAddress = fmt.Sprintf("drone-%s-svc", request.DroneID)
		request.MAVLinkUDPPort = 14560
	case "local", "remote":
	default:
		writeBadRequest(w, `location must be one of "cluster", "local" or "remote"`, nil)
		return
	}
	requestBody, err := json.Marshal(obj{
		"drone_location":   request.Location,
		"device_id":        request.DroneID,
		"mavlink_address":  request.MAVLinkAddress,
		"mavlink_udp_port": request.MAVLinkUDPPort,
		"video_udp_port":   5600,
		"pos_x":            request.PosX,
		"pos_y":            request.PosY,
		"pos_z":            request.PosZ,
		"yaw":              request.Yaw,
		"pitch":            request.Pitch,
		"roll":             request.Roll,
	})
	if err != nil {
		writeServerError(w, "failed to add drone", err)
		return
	}

	droneURL := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/drones", simulationName)
	resp, err := http.Post(droneURL, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		writeServerError(w, "Could not add drone to gzserver", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := ioutil.ReadAll(resp.Body)
		writeServerError(w, fmt.Sprintf("Could not add drone to gzserver (%d): %s", resp.StatusCode, msg), nil)
		return
	}
	writeJSON(w, obj{"drone_id": request.DroneID})
	creationSucceeded = true
}

func generateCertificate() (privateKey, publicKey string, err error) {
	priv, err := rsa.GenerateKey(cryptoRand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("could not generate private rsa key: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "unused",
		},
	}
	cert, err := x509.CreateCertificate(cryptoRand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("could not generate certificate: %w", err)
	}
	out := bytes.Buffer{}
	pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: cert})
	publicKey = out.String()
	out.Reset()
	pem.Encode(&out, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	privateKey = out.String()
	return privateKey, publicKey, nil
}

var droneIDAlphabet = []string{
	"alpha",
	"bravo",
	"charlie",
	"delta",
	"echo",
	"foxtrot",
	"golf",
	"hotel",
	"india",
	"juliet",
	"kilo",
	"lima",
	"mike",
	"november",
	"oscar",
	"papa",
	"quebec",
	"romeo",
	"sierra",
	"tango",
	"uniform",
	"victor",
	"whiskey",
	"xray",
	"yankee",
	"zulu",
}

func generateDroneID(c context.Context, simulationName string) (string, error) {
	drones, err := getDrones(c, simulationName)
	if err != nil {
		return "", fmt.Errorf("could not get existing drone ids: %w", err)
	}
	availableLetters := make([]string, 0)
	for _, l := range droneIDAlphabet {
		available := true
		for _, d := range drones {
			if strings.HasPrefix(d.DeviceID, l) {
				available = false
				break
			}
		}
		if !available {
			continue
		}
		availableLetters = append(availableLetters, l)
	}
	if len(availableLetters) != 0 {
		// still free letters
		return availableLetters[rand.Intn(len(availableLetters))], nil
	}

	// no free letters
	for i := 0; i < 20; i++ {
		letter := droneIDAlphabet[rand.Intn(len(droneIDAlphabet))]

		id := fmt.Sprintf("%s%s", letter, strings.Split(uuid.New().String(), "-")[3])
		used := false
		for _, d := range drones {
			if d.DeviceID == id {
				used = true
				break
			}
		}
		if used {
			continue
		}
		return id, nil
	}

	return "", errors.New("could not generate unique drone id in 20 tries")
}

type drone struct {
	DeviceID      string `json:"device_id"`
	DroneLocation string `json:"drone_location"`
}

func getDrones(ctx context.Context, simulationName string) ([]drone, error) {
	var resp []drone
	url := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/drones", simulationName)
	if err := getJSON(ctx, url, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func waitHostPort(c context.Context, namespace, serviceName string) (string, error) {
	kube := client.Clientset
	for {
		lb, err := kube.CoreV1().Services(namespace).Get(c, serviceName, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		if len(lb.Status.LoadBalancer.Ingress) > 0 {
			return fmt.Sprint(lb.Status.LoadBalancer.Ingress[0].IP, ":", lb.Spec.Ports[0].Port), nil
		}

		select {
		case <-c.Done():
			// timeout/cancel
			return "", c.Err()
		case <-time.After(500 * time.Millisecond):
			// continue polling
		}
	}
}

func waitDeploymentAvailable(ctx context.Context, namespace, name string) error {
	for {
		dep, err := client.Clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			if dep.Status.AvailableReplicas > 0 {
				return nil
			}
		} else if !k8serrors.IsNotFound(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func getShortestUniqueSimulationName(c context.Context, names []string) string {
	sims, err := client.GetSimulations(c)
	if err != nil {
		panic(err)
	}
	// start from shortest possible and end when first unique is found with that length
	for i := 5; i < 22; i++ {
		for _, name := range names {
			nameCandidate := name[:i]
			found := true
			for _, sim := range sims {
				if strings.HasPrefix(sim.Name, nameCandidate) {
					found = false
					break
				}
			}
			if found {
				return nameCandidate
			}
		}
	}
	panic("Could not find unique name")
}

func generateSimulationName(c context.Context) string {
	names := make([]string, 20)
	for i := 0; i < 20; i++ {
		names[i] = fmt.Sprintf("sim-%s", uuid.New().String())
	}

	return getShortestUniqueSimulationName(c, names)
}

func generateSimulationID(name string) string {
	return name + "-" + time.Now().UTC().Format(time.RFC3339)
}

func getDronesHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")

	resp, err := getDrones(c, simulationName)
	if err != nil {
		writeServerError(w, "could not get list of drones", err)
		return
	}
	writeJSON(w, obj{"drones": resp})
}

func commandDroneHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "invalid request body", err)
		return
	}
	switch req.Command {
	case "takeoff", "land":
	default:
		writeBadRequest(w, "unsupported command", nil)
		return
	}

	simType, err := client.GetSimulationType(c, simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	mqttServer := mqttServerURL
	if simType == kube.SimulationStandalone {
		mqttServer = fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)
	}
	client, err := getIotCoreClient(c, mqttServer)
	if err != nil {
		writeServerError(w, "failed to connect to command server", err)
		return
	}
	defer client.Close()
	err = client.SendCommand(c, simulationName, droneID, "control", obj{
		"Command":   req.Command,
		"Timestamp": time.Now(),
	})
	if err != nil {
		writeServerError(w, "could not publish command", err)
	}
}

func droneLogStreamHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	logs, err := client.GetDroneLogs(c, simulationName, droneID)
	if errors.Is(err, kube.ErrNoSuchDrone) {
		writeNotFound(w, "the requested drone does not exist", nil)
	} else if err != nil {
		writeServerError(w, "failed to retreive logs", err)
	} else if _, err := w.Write(logs); err != nil {
		log.Println("failed to send logs to client:", err)
	}
}

func droneEventStreamHandler(w http.ResponseWriter, r *http.Request) {
	c, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	conn, err := acceptWebsocket(w, r)
	if err != nil {
		writeBadRequest(w, "failed to upgrade connection to websocket", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	var req struct {
		Path string `json:"path"`
	}
	if err = conn.ReadJSON(c, &req); err != nil {
		conn.WriteError(c, "failed to read input message", err)
		return
	}
	if req.Path == "" || req.Path[0] != '/' {
		req.Path = "/" + req.Path
	}
	if req.Path[len(req.Path)-1] != '/' {
		req.Path = req.Path + "/"
	}

	simType, err := client.GetSimulationType(c, simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	mqttServer := mqttServerURL
	if simType == kube.SimulationStandalone {
		mqttServer = fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)
	}
	client, err := getIotCoreClient(c, mqttServer)
	if err != nil {
		writeServerError(w, "failed to connect to command server", err)
		return
	}
	defer client.Close()
	err = client.Subscribe(c, simulationName, droneID, req.Path, func(msg *subscriptionMessage) {
		if err := conn.WriteJSON(c, msg); err != nil {
			log.Println("failed to send event:", err)
			cancel()
		}
	})
	if err != nil {
		conn.WriteError(c, "Could not subscribe", err)
		return
	}
}

func droneVideoStreamHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := httprouter.ParamsFromContext(ctx)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	simType, err := client.GetSimulationType(ctx, simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	mqttServer := mqttServerURL
	videoServer := *videoServerURL.URL
	if simType == kube.SimulationStandalone {
		mqttServer = fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)
		videoServer = url.URL{Scheme: "rtsp"}
		videoServer.Host, err = waitHostPort(ctx, simulationName, "video-server-public-svc")
		if err != nil {
			writeServerError(w, "error getting video server address", err)
			return
		}
	}
	videoServer.Path = "/" + droneID

	client, err := getIotCoreClient(ctx, mqttServer)
	if err != nil {
		writeServerError(w, "failed to connect to command server", err)
		return
	}
	defer client.Close()
	err = client.SendCommand(ctx, simulationName, droneID, "videostream", obj{
		"Command": "start",
		"Address": urlWithAuth(videoServer),
	})
	if err != nil {
		writeServerError(w, "could not publish command", err)
		return
	}
	videoServer.Scheme = "rtsp"
	if videoServer.Port() == "8555" {
		videoServer.Host = videoServer.Hostname() + ":8554"
	}
	writeJSON(w, obj{"video_url": videoServer.String()})
}

func droneVideoStreamWebUIHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	prefix := "/simulations/" + simulationName + "/video"
	http.StripPrefix(prefix, httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: "http",
		Host:   "video-streamer-svc." + simulationName,
	})).ServeHTTP(w, r)
}

func droneShellHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(ctx)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	podName, err := client.GetDronePodName(ctx, simulationName, droneID)
	if errors.Is(err, kube.ErrNoSuchDrone) {
		writeNotFound(w, "the drone does not exist", nil)
		return
	} else if err != nil {
		writeServerError(w, "could not find pod from cluster", err)
		return
	}
	clientConfig, err := rest.InClusterConfig()
	if err != nil {
		writeServerError(w, "failed to get cluster config", err)
		return
	}
	conn, err := acceptWebsocket(w, r)
	if err != nil {
		writeBadRequest(w, "failed to upgrade connection to websocket", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	shell := newRemoteShell(conn, clientConfig)
	err = shell.Start(ctx, simulationName, podName)
	if err != nil && !errors.Is(err, context.Canceled) {
		conn.WriteError(ctx, "remote shell error", err)
	}
}

type remoteShell struct {
	conn       *websocketConn
	restConfig *rest.Config
	inWriter   io.WriteCloser
	sizeQueue  chan *remotecommand.TerminalSize
	ctx        context.Context
}

func newRemoteShell(conn *websocketConn, restConfig *rest.Config) *remoteShell {
	return &remoteShell{
		conn:       conn,
		restConfig: restConfig,
	}
}

func (s *remoteShell) Start(ctx context.Context, ns, pod string) (err error) {
	var cancel context.CancelFunc
	s.ctx, cancel = context.WithCancel(ctx)
	defer cancel()
	var inReader io.ReadCloser
	inReader, s.inWriter = io.Pipe()
	defer inReader.Close()
	defer s.inWriter.Close()
	req := client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(ns).
		SubResource("exec")
	req.VersionedParams(&v1.PodExecOptions{
		Command: []string{"bash"},
		Stdin:   true,
		Stdout:  true,
		Stderr:  true,
		TTY:     true,
	}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(s.restConfig, "POST", req.URL())
	if err != nil {
		return err
	}
	errch := make(chan error, 1)
	go func() (err error) {
		defer func() {
			cancel()
			errch <- err
		}()
		return exec.Stream(remotecommand.StreamOptions{
			Stdin:             inReader,
			Stdout:            s,
			Stderr:            s,
			TerminalSizeQueue: s,
			Tty:               true,
		})
	}()
	return multierror.Append(s.handleInput(), <-errch).ErrorOrNil()
}

func (s *remoteShell) handleInput() error {
	s.sizeQueue = make(chan *remotecommand.TerminalSize)
	defer close(s.sizeQueue)
	for {
		_, data, err := s.conn.Read(s.ctx)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("invalid message %s", data)
		}
		switch data[0] {
		case 'd': // message contains data from client stdin
			if _, err = s.inWriter.Write(data[1:]); err != nil {
				return err
			}
		case 's': // message contains terminal size change event
			var sizeChange remotecommand.TerminalSize
			if err = json.Unmarshal(data[1:], &sizeChange); err != nil {
				return err
			}
			select {
			case <-s.ctx.Done():
				return s.ctx.Err()
			case s.sizeQueue <- &sizeChange:
			}
		default: // unknown message type
			return fmt.Errorf("invalid input message type: %c (%[1]d)", data[0])
		}
	}
}

func (s *remoteShell) Write(p []byte) (int, error) {
	wr, err := s.conn.Writer(s.ctx, websocket.MessageBinary)
	if err != nil {
		return 0, err
	}
	defer wr.Close()
	return wr.Write(p)
}

func (s *remoteShell) Next() *remotecommand.TerminalSize {
	return <-s.sizeQueue
}

func refreshSimulationExpiry(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := httprouter.ParamsFromContext(r.Context())
		name := params.ByName("simulationName")
		err := client.RefreshSimulationExpiryTime(r.Context(), name)
		if err != nil {
			log.Printf(`an error occurred when refreshing expiry time of simulation "%s": %v`, name, err)
		}
		next(w, r)
	})
}

type userJWTClaims struct {
	jwt.StandardClaims
	Roles []string
}

func (c *userJWTClaims) hasAccess(owners kube.StringSet) bool {
	if owners[c.Subject] {
		return true
	}
	for _, role := range c.Roles {
		if owners[role] || role == simulationAdminRole {
			return true
		}
	}
	return false
}

func checkSimulationAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := httprouter.ParamsFromContext(r.Context())
		simulationName := params.ByName("simulationName")
		claims, err := getClaimsFromRequest(r)
		if err != nil {
			writeUnauthorized(w, "invalid Authorization header", err)
			return
		}
		owners, err := client.GetSimulationOwners(r.Context(), simulationName)
		if errors.Is(err, kube.ErrSimulationDoesntExist) {
			writeNotFound(w, "simulation doesn't exist", nil)
		} else if err != nil {
			writeServerError(w, "failed to access simulation", nil)
		} else if claims.hasAccess(owners) {
			next.ServeHTTP(w, r)
		} else {
			writeNotFound(w, "simulation doesn't exist", nil)
		}
	})
}

func getClaimsFromRequest(r *http.Request) (*userJWTClaims, error) {
	if !enableAuth {
		return &userJWTClaims{
			StandardClaims: jwt.StandardClaims{
				Subject: "simulation-coordinator",
			},
			Roles: []string{simulationAdminRole},
		}, nil
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var claims userJWTClaims
	var parser jwt.Parser
	// The token has already been validated and verified by firebase-auth-sidecar.
	parser.SkipClaimsValidation = true
	if _, _, err := parser.ParseUnverified(token, &claims); err != nil {
		return nil, err
	}
	if claims.Subject == "" {
		return nil, errors.New("token is missing Subject field")
	}
	return &claims, nil
}
