package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/julienschmidt/httprouter"
)

var mqttClient mqtt.Client
var mqttPub MqttPublisher
var projectID string

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: web-backend <mqtt-broker-address> | cloud-push | cloud-pull")
		return
	}

	projectID = os.Getenv("PROJECT_ID")
	if projectID == "" {
		projectID = "auto-fleet-mgnt"
	}

	mqttBrokerAddress := os.Args[1]
	if mqttBrokerAddress == "cloud-pull" {
		log.Println("MQTT: IoT Core pull")
		mqttPub = NewIoTPublisher()
		// go pullIoTCoreMessages("telemetry-web-backend-pull-sub")
		go pullIoTCoreMessages("iot-device-telemetry-web-backend-pull-sub")
		go pullIoTCoreMessages("iot-device-debug-values-web-backend-pull-sub")
		go pullIoTCoreMessages("iot-device-debug-events-web-backend-pull-sub")
	} else if mqttBrokerAddress == "cloud-push" {
		log.Println("MQTT: IoT Core push")
		mqttPub = NewIoTPublisher()
	} else {
		log.Printf("MQTT: emulator @ %s", mqttBrokerAddress)
		mqttClient := newMQTTClient("web-backend", mqttBrokerAddress)
		defer mqttClient.Disconnect(1000)
		listenMQTTEvents(mqttClient)
		mqttPub = NewMqttPublisher(mqttClient)
	}

	router := httprouter.New()
	registerRoutes(router)
	if mqttBrokerAddress == "cloud-pull" || mqttBrokerAddress == "cloud-push" {
		registerCloudRoutes(router)
	} else {
		registerMinikubeRoutes(router)
	}

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

	port := "8083"
	log.Printf("Listening on port %s", port)
	err := http.ListenAndServe(":"+port, setCORSHeader(router))
	if err != nil {
		log.Fatal(err)
	}

	return
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
