package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/tiiuae/fleet-management/simulation-coordinator/kube"
	v1 "k8s.io/api/core/v1"
)

var (
	imageGZServer                   = "ghcr.io/tiiuae/tii-gzserver:dev"
	imageGZWeb                      = "ghcr.io/tiiuae/tii-gzweb:latest"
	imageFogDrone                   = "ghcr.io/tiiuae/tii-fog-drone:dev"
	imageMQTTServer                 = "ghcr.io/tiiuae/tii-mqtt-server:latest"
	imageMissionControl             = "ghcr.io/tiiuae/tii-mission-control:latest"
	imageVideoServer                = "ghcr.io/tiiuae/tii-video-server:latest"
	imageVideoStreamer              = "ghcr.io/tiiuae/tii-video-streamer:latest"
	imageWebBackend                 = "ghcr.io/tiiuae/tii-web-backend:latest"
	imageMissionDataRecorderBackend = "ghcr.io/tiiuae/tii-mission-data-recorder-backend:latest"
	imageUpdateAgent                = "ghcr.io/tiiuae/tii-update-agent-kube:latest"

	defaultPullPolicy = pullPolicyValue{
		value: func() *v1.PullPolicy {
			p := v1.PullIfNotPresent
			return &p
		}(),
	}
)

var (
	mqttServerURL = "ssl://mqtt.googleapis.com:8883"

	videoServerURL = urlValue{URL: &url.URL{
		Scheme: "rtsps",
		Host:   "video-stream.sacplatform.com:8555",
	}}
	videoServerUsername = "DroneUser"
	videoServerPassword = "22f6c4de-6144-4f6c-82ea-8afcdf19f316"

	pubsubSubscriptions = strings.Join([]string{
		"iot-device-debug-events-simulation-coordinator",
		"iot-device-debug-values-simulation-coordinator",
		"iot-device-imu-simulation-coordinator",
		"iot-device-location-simulation-coordinator",
		"iot-device-telemetry-simulation-coordinator",
	}, ",")
	cloudSimulationCoordinatorURL = "https://simulation.sacplatform.com"
	cloudMode                     = false

	missionDataRecorederBackendCloudURL = "https://mission-data-upload.webapi.sacplatform.com"
	missionControlURL                   = "https://missions.webapi.sacplatform.com"
	deviceManagementURL                 = "https://devices.webapi.sacplatform.com"
)

var (
	projectID = "auto-fleet-mgnt"
	// registryID = "fleet-registry"
	region = "europe-west1"
)

func getRegistryID(simulationType kube.SimulationType, simulationName string) string {
	// TODO: fleet-registry -> tenantID
	if simulationType == kube.SimulationGlobal {
		return fmt.Sprintf("fleet-registry~s~%s", simulationName)
	}

	// Default standalone registryID
	return "fleet-registry"
}

var (
	standaloneMissionDataBucket       = "simulation-mission-data"
	missionDataRecorderBackendKeyPath = "/secrets/mission-data-recorder-backend.json"
	missionDataRecorderBackendKey     = "" // Read from missionDataRecorderBackendKeyPath
	storeStandaloneMissionDataLocally = true
)

var (
	port                   = 8087
	dockerConfigSecretName = "dockerconfigjson"

	// Read from SIMULATION_COORDINATOR_NAMESPACE environment variable. This is
	// passed as an environment variable instead of a command line parameter
	// because Kubernetes Downwards API does not support command line
	// parameters.
	currentNamespace         string
	defaultSimulationGPUMode = kube.SimulationGPUModeNone
	defaultExpiryDuration    = 2 * 24 * time.Hour
	expiryCheckInterval      = time.Hour

	enableAuth     = true
	outClusterMode = false
)

func init() {
	flag.StringVar(&imageGZServer, "image-gzserver", imageGZServer, "Docker image for gazebo server")
	flag.StringVar(&imageGZWeb, "image-gzweb", imageGZWeb, "Docker image for gazebo web client")
	flag.StringVar(&imageFogDrone, "image-drone", imageFogDrone, "Docker image for drone")
	flag.StringVar(&imageMQTTServer, "image-mqtt-server", imageMQTTServer, "Docker image for MQTT server")
	flag.StringVar(&imageMissionControl, "image-mission-control", imageMissionControl, "Docker image for mission control")
	flag.StringVar(&imageVideoServer, "image-video-server", imageVideoServer, "Docker image for video server")
	flag.StringVar(&imageVideoStreamer, "image-video-streamer", imageVideoStreamer, "Docker image for video streamer")
	flag.StringVar(&imageWebBackend, "image-web-backend", imageWebBackend, "Docker image for web backend")
	flag.StringVar(&imageMissionDataRecorderBackend, "image-mission-data-recorder-backend", imageMissionDataRecorderBackend, "Docker image for mission data recorder backend")

	flag.Var(&defaultPullPolicy, "pull-policy", `Kubernetes pull policy used for deployments. Supported values are "Always", "IfNotPresent" and "Never".`)

	flag.StringVar(&mqttServerURL, "mqtt-server-url", mqttServerURL, "URL of the MQTT server")
	flag.Var(&videoServerURL, "video-server-url", "URL of the video server")
	flag.StringVar(&videoServerUsername, "video-server-username", videoServerUsername, "Username used to log in to the video server")
	flag.StringVar(&videoServerPassword, "video-server-password", videoServerPassword, "Password used to log in to the video server")
	flag.StringVar(&pubsubSubscriptions, "events-subscriptions", pubsubSubscriptions, "Comma-separated list of Google Pub/Sub subscriptions to listen for device events.")
	flag.StringVar(&cloudSimulationCoordinatorURL, "simulation-coordinator-url", cloudSimulationCoordinatorURL, "URL of simulation-coordinator instance in the cloud")
	flag.BoolVar(&cloudMode, "cloud-mode", cloudMode, "If true, subscribes to the Google Pub/Sub subscriptions specified by -events-subscription and provides them in an endpoint. If false, uses mqtt-broker found in local Kubernetes cluster.")
	flag.StringVar(&missionDataRecorederBackendCloudURL, "mission-data-recorder-backend-url", missionDataRecorederBackendCloudURL, "URL of mission data recorder backend server")
	flag.StringVar(&missionControlURL, "mission-control-url", missionControlURL, "URL of mission control backend server")
	flag.StringVar(&deviceManagementURL, "device-management-url", deviceManagementURL, "URL of device management backend server")

	flag.StringVar(&projectID, "project-id", projectID, "Google Cloud project ID")
	// flag.StringVar(&registryID, "registry-id", registryID, "Google Cloud IoT Core registry ID")
	flag.StringVar(&region, "region", region, "Google Cloud region")

	flag.StringVar(&standaloneMissionDataBucket, "standalone-mission-data-bucket", standaloneMissionDataBucket, "Name of the bucket where mission data is stored in standalone simulations")
	flag.StringVar(&missionDataRecorderBackendKeyPath, "mission-data-recorder-backend-key", missionDataRecorderBackendKeyPath, "Path to the JSON key used to upload mission data to buckets")
	flag.BoolVar(&storeStandaloneMissionDataLocally, "store-standalone-mission-data-locally", storeStandaloneMissionDataLocally, "If true, mission data for standalone simulations is stored in the local file system. If false, it is stored in a Google Cloud Bucket.")

	flag.IntVar(&port, "port", port, "Port to listen to")
	flag.StringVar(&dockerConfigSecretName, "docker-config-secret", dockerConfigSecretName, "The name of the secret to use for pulling images. It must be in the namespace specified in the environment variable SIMULATION_COORDINATOR_NAMESPACE.")
	flag.Var(&defaultSimulationGPUMode, "default-gpu-mode", "The default GPU mode for new simulations that don't specify the GPU mode to use")
	flag.DurationVar(&defaultExpiryDuration, "simulation-expiry-duration", defaultExpiryDuration, "Simulations will be automatically removed after this duration if they have not been interacted with")
	flag.DurationVar(&expiryCheckInterval, "expiry-check-interval", expiryCheckInterval, "How often simulations are checked for expiration")
	flag.BoolVar(&enableAuth, "enable-auth", enableAuth, "If true, the API requires authentication using a JWT")
	flag.BoolVar(&outClusterMode, "out-cluster", outClusterMode, "If true, uses out-of-cluster config")
}

func urlWithAuth(u url.URL) string {
	u.User = url.UserPassword(videoServerUsername, videoServerPassword)
	return u.String()
}

type urlValue struct {
	URL *url.URL
}

func (v urlValue) String() string {
	if v.URL != nil {
		return v.URL.String()
	}
	return ""
}

func (v urlValue) Set(s string) error {
	if u, err := url.Parse(s); err != nil {
		return err
	} else {
		*v.URL = *u
	}
	return nil
}

type pullPolicyValue struct {
	value *v1.PullPolicy
}

func (v pullPolicyValue) String() string {
	if v.value == nil {
		return ""
	}
	return string(*v.value)
}

func (v pullPolicyValue) Set(s string) error {
	switch v1.PullPolicy(s) {
	case v1.PullAlways, v1.PullIfNotPresent, v1.PullNever:
		*v.value = v1.PullPolicy(s)
		return nil
	default:
		return fmt.Errorf(`unsupported pull policy: "%s"`, s)
	}
}

func withSignals(ctx context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	c := make(chan os.Signal, 1)
	go func() {
		<-c
		cancel()
	}()
	signal.Notify(c, signals...)
	return ctx, cancel
}

func main() {
	flag.Parse()

	ctx, cancel := withSignals(context.Background(), os.Interrupt)
	defer cancel()
	var wg sync.WaitGroup

	currentNamespace = os.Getenv("SIMULATION_COORDINATOR_NAMESPACE")
	if currentNamespace == "" {
		currentNamespace = "dronsole"
		log.Println("Environment variable SIMULATION_COORDINATOR_NAMESPACE is not defined -> defaulting to 'dronsole'")
	}

	var err error
	if outClusterMode {
		client, err = kube.NewOutClusterConfig()
		if err != nil {
			log.Fatalln("failed to create Kubernetes client:", err)
		}

	} else {
		client, err = kube.NewInClusterConfig()
		if err != nil {
			log.Fatalln("failed to create Kubernetes client:", err)
		}
	}
	client.PullPolicy = *defaultPullPolicy.value

	if cloudMode {
		var err error
		subMan, err = newSubscriptionManager(ctx)
		if err != nil {
			log.Fatalln("failed to start Pub/Sub manager:", err)
		}
		wg.Add(1)
		go func() {
			defer func() {
				cancel()
				wg.Done()
			}()
			err := subMan.subscribeToPubsub(ctx, strings.Split(pubsubSubscriptions, ","))
			if err != nil {
				log.Println("Pub/Sub manager exited with an error:", err)
			}
		}()
	}
	if expiryCheckInterval != 0 {
		wg.Add(1)
		go func() {
			defer func() {
				cancel()
				wg.Done()
			}()
			expiryWatcher(ctx, expiryCheckInterval)
		}()
	}

	if !storeStandaloneMissionDataLocally {
		recorderBackendKey, err := ioutil.ReadFile(missionDataRecorderBackendKeyPath)
		if err != nil {
			log.Println("failed to read mission data recorder backend key:", err)
			return
		}
		missionDataRecorderBackendKey = string(recorderBackendKey)
	}

	router := httprouter.New()
	registerRoutes(router, enableAuth)
	router.HandleMethodNotAllowed = true
	router.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeNotFound(w, "not found", nil)
	})
	router.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, "method not allowed", nil, http.StatusMethodNotAllowed)
	})
	router.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isValidOrigin(r) && r.Header.Get("Access-Control-Request-Method") != "" {
			// Set CORS headers
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "3600")
		}

		// Adjust status code to 204
		w.WriteHeader(http.StatusNoContent)
	})

	log.Println("Listening on port", port)
	server := http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: setCORSHeader(router),
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("listen returned an error:", server.ListenAndServe())
	}()
	<-ctx.Done()
	if err := server.Shutdown(ctx); err != nil {
		log.Println("shutdown returned an error:", err)
	}
	wg.Wait()
}

func setCORSHeader(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isValidOrigin(r) {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		}
		handler.ServeHTTP(w, r)
	})
}

func isValidOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return strings.HasSuffix(o, "localhost:8080") || strings.HasSuffix(o, "sacplatform.com")
}

func expiryWatcher(ctx context.Context, interval time.Duration) error {
	for {
		if err := client.RemoveExpiredSimulations(ctx); err != nil {
			log.Println("an error occurred while removing expired simulations:", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
