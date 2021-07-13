package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/julienschmidt/httprouter"
)

var (
	imageGZServer         = "ghcr.io/tiiuae/tii-gzserver"
	imageGZWeb            = "ghcr.io/tiiuae/tii-gzweb"
	imageFogDrone         = "ghcr.io/tiiuae/tii-fog-drone:f4f-int"
	imageMQTTServer       = "ghcr.io/tiiuae/tii-mqtt-server:latest"
	imageMissionControl   = "ghcr.io/tiiuae/tii-mission-control:latest"
	imageVideoServer      = "ghcr.io/tiiuae/tii-video-server:latest"
	imageVideoMultiplexer = "ghcr.io/tiiuae/tii-video-multiplexer:latest"
	imageWebBackend       = "ghcr.io/tiiuae/tii-web-backend:latest"
)

var (
	mqttServerURL = "ssl://mqtt.googleapis.com:8883"

	videoServerHost     = "video-stream.sacplatform.com:8554"
	videoServerUsername = "DroneUser"
	videoServerPassword = "22f6c4de-6144-4f6c-82ea-8afcdf19f316"

	pubsubSubscriptions = strings.Join([]string{
		"iot-device-debug-events-simulation-coordinator",
		"iot-device-debug-values-simulation-coordinator",
		"iot-device-imu-simulation-coordinator",
		"iot-device-location-simulation-coordinator",
		"iot-device-telemetry-simulation-coordinator",
	}, ",")
	eventsAPIURL      = "https://simulation.sacplatform.com"
	eventsAPIWSURL    = "" // Automatically set based on eventsAPIURL
	enableEventPubsub = false
)

var (
	projectID  = "auto-fleet-mgnt"
	registryID = "fleet-registry"
	region     = "europe-west1"
)

var (
	port                   = 8087
	dockerConfigSecretName = "dockerconfigjson"

	// Read from DOCKERCONFIG_SECRET_NAMESPACE environment variable. This is
	// passed as an environment variable instead of a command line parameter
	// because Kubernetes Downwards API does not support command line
	// parameters.
	currentNamespace string
)

func init() {
	flag.StringVar(&imageGZServer, "image-gzserver", imageGZServer, "Docker image for gazebo server")
	flag.StringVar(&imageGZWeb, "image-gzweb", imageGZWeb, "Docker image for gazebo web client")
	flag.StringVar(&imageFogDrone, "image-drone", imageFogDrone, "Docker image for drone")
	flag.StringVar(&imageMQTTServer, "image-mqtt-server", imageMQTTServer, "Docker image for MQTT server")
	flag.StringVar(&imageMissionControl, "image-mission-control", imageMissionControl, "Docker image for mission control")
	flag.StringVar(&imageVideoServer, "image-video-server", imageVideoServer, "Docker image for video server")
	flag.StringVar(&imageVideoMultiplexer, "image-video-multiplexer", imageVideoMultiplexer, "Docker image for video multiplexer")
	flag.StringVar(&imageWebBackend, "image-web-backend", imageWebBackend, "Docker image for web backend")

	flag.StringVar(&mqttServerURL, "mqtt-server-url", mqttServerURL, "URL of the MQTT server")
	flag.StringVar(&videoServerHost, "video-server-host", videoServerHost, "Hostname/ip and port of the video server")
	flag.StringVar(&videoServerUsername, "video-server-username", videoServerUsername, "Username used to log in to the video server")
	flag.StringVar(&videoServerPassword, "video-server-password", videoServerPassword, "Password used to log in to the video server")
	flag.StringVar(&pubsubSubscriptions, "events-subscriptions", pubsubSubscriptions, "Comma-separated list of Google Pub/Sub subscriptions to listen for device events.")
	flag.StringVar(&eventsAPIURL, "events-api-url", eventsAPIURL, "URL of events API")
	flag.BoolVar(&enableEventPubsub, "events-enable", enableEventPubsub, "If true, subscribes to the Google Pub/Sub subscriptions specified by -events-subscription and provides them in an endpoint. If false, uses mqtt-broker found in local Kubernetes cluster.")

	flag.StringVar(&projectID, "project-id", projectID, "Google Cloud project ID")
	flag.StringVar(&registryID, "registry-id", registryID, "Google Cloud IoT Core registry ID")
	flag.StringVar(&region, "region", region, "Google Cloud region")

	flag.IntVar(&port, "port", port, "Port to listen to")
	flag.StringVar(&dockerConfigSecretName, "docker-config-secret", dockerConfigSecretName, "The name of the secret to use for pulling images. It must be in the same namespace as the simulation-coordinator pod.")
}

type SimulationGPUMode int

const (
	SimulationGPUModeNone SimulationGPUMode = iota
	SimulationGPUModeNvidia
)

var simulationGPUMode SimulationGPUMode

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
	// SIMULATION_GPU_MODE should be on of following:
	// - none (or empty)
	// - nvidia
	switch os.Getenv("SIMULATION_GPU_MODE") {
	case "nvidia":
		simulationGPUMode = SimulationGPUModeNvidia
	}

	ctx, cancel := withSignals(context.Background(), os.Interrupt)
	defer cancel()
	wait := make(chan struct{}, 2)

	eventsAPIWSURL = strings.Replace(eventsAPIURL, "http", "ws", 1)

	currentNamespace = os.Getenv("DOCKERCONFIG_SECRET_NAMESPACE")
	if currentNamespace == "" {
		log.Fatalln("Environment variable DOCKERCONFIG_SECRET_NAMESPACE is not defined")
	}

	if enableEventPubsub {
		var err error
		subMan, err = newSubscriptionManager(ctx)
		if err != nil {
			log.Fatalln("failed to start Pub/Sub manager:", err)
		}
		go func() {
			defer func() { wait <- struct{}{} }()
			for ctx.Err() == nil {
				err := func() (err interface{}) {
					defer func() {
						if r := recover(); r != nil {
							err = r
						}
					}()
					return subMan.subscribeToPubsub(
						ctx,
						strings.Split(pubsubSubscriptions, ","),
					)
				}()
				if err != nil {
					log.Println("Pub/Sub manager exited with an error:", err)
				}
			}
		}()
	}

	router := httprouter.New()
	registerRoutes(router)
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
	go func() {
		defer func() { wait <- struct{}{} }()
		for ctx.Err() == nil {
			log.Println("listen returned an error:", server.ListenAndServe())
		}
	}()
	<-ctx.Done()
	if err := server.Shutdown(ctx); err != nil {
		log.Println("shutdown returned an error:", err)
	}
	<-wait
	<-wait
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
