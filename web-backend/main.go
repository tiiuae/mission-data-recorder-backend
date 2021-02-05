package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/julienschmidt/httprouter"
)

var mqttClient mqtt.Client

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: web-backend <mqtt-broker-address>")
		return
	}
	mqttBrokerAddress := os.Args[1]

	port := "8083"

	mqttClient = newMQTTClient(mqttBrokerAddress)
	defer mqttClient.Disconnect(1000)
	router := httprouter.New()
	registerRoutes(router)

	listenMQTTEvents(mqttClient)

	log.Printf("Listening on port %s", port)
	err := http.ListenAndServe(":"+port, router)
	if err != nil {
		log.Fatal(err)
	}

	return
}

func listenMQTTEvents(client mqtt.Client) {
	const qos = 0
	token := client.Subscribe("/devices/#", qos, func(client mqtt.Client, msg mqtt.Message) {
		t := strings.TrimPrefix(msg.Topic(), "/devices/")
		deviceID := strings.Split(t, "/")[0]
		topic := strings.TrimPrefix(t, deviceID+"/")
		if strings.HasPrefix(topic, "events") {
			// we have a message from the device
			handleMQTTEvent(context.Background(), deviceID, strings.TrimPrefix(topic, "events/"), msg)
		}
	})

	err := token.Error()
	if err != nil {
		log.Fatalf("Could not subscribe to MQTT events: %v", err)
	}
}
func handleMQTTEvent(c context.Context, deviceID string, eventTopic string, msg mqtt.Message) {
	switch eventTopic {
	case "location":
		go handleLocationEvent(c, deviceID, msg.Payload())
	}
}

func newMQTTClient(brokerAddress string) mqtt.Client {
	opts := mqtt.NewClientOptions().
		AddBroker(brokerAddress).
		SetClientID("web-backend").
		SetUsername("web-backend").
		//SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}).
		SetPassword("").
		SetProtocolVersion(4) // Use MQTT 3.1.1

	client := mqtt.NewClient(opts)

	tok := client.Connect()
	if err := tok.Error(); err != nil {
		log.Fatalf("MQTT connection failed: %v", err)
	}
	if !tok.WaitTimeout(time.Second * 5) {
		log.Fatal("MQTT connection timeout")
	}
	err := tok.Error()
	if err != nil {
		log.Fatalf("Could not connect to MQTT broker: %v", err)
	}

	return client
}
