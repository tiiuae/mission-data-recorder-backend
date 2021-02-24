package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/julienschmidt/httprouter"
)

var mqttClient mqtt.Client

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: web-backend <mqtt-broker-address> | cloud-push | cloud-pull")
		return
	}

	mqttBrokerAddress := os.Args[1]
	if mqttBrokerAddress == "cloud-pull" {
		log.Println("MQTT: IoT Core pull")
		// go pullIoTCoreMessages("telemetry-web-backend-pull-sub")
		go pullIoTCoreMessages("iot-device-location-web-backend-pull-sub")
	} else if mqttBrokerAddress == "cloud-push" {
		log.Println("MQTT: IoT Core push")
	} else {
		log.Printf("MQTT: emulator @ %s", mqttBrokerAddress)
		mqttClient := newMQTTClient("web-backend", mqttBrokerAddress)
		defer mqttClient.Disconnect(1000)
		listenMQTTEvents(mqttClient)
	}

	router := httprouter.New()
	registerRoutes(router)
	if mqttBrokerAddress == "cloud-pull" || mqttBrokerAddress == "cloud-push" {
		registerCloudRoutes(router)
	} else {
		registerMinikubeRoutes(router)
	}

	router.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Access-Control-Request-Method") != "" {
			// Set CORS headers
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:8080")
			w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "3600")
		}

		// Adjust status code to 204
		w.WriteHeader(http.StatusNoContent)
	})

	port := "8083"
	log.Printf("Listening on port %s", port)
	err := http.ListenAndServe(":"+port, setCORSHeader("http://localhost:8080", router))
	if err != nil {
		log.Fatal(err)
	}

	return
}

func setCORSHeader(origin string, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		handler.ServeHTTP(w, r)
	})
}
