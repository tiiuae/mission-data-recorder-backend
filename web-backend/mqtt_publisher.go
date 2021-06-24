package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"google.golang.org/api/cloudiot/v1"
)

const (
	registryID    = "fleet-registry"
	region        = "europe-west1"
	algorithm     = "RS256"
	defaultServer = "ssl://mqtt.googleapis.com:8883"
	qos           = 1 // QoS 2 isn't supported in GCP
	retain        = false
	username      = "unused" // always this value in GCP
)

type MqttPublisher interface {
	SendCommand(deviceID string, subfolder string, payload []byte) error
}

type mqttPublisher struct {
	client mqtt.Client
}
type iotPublisher struct{}

func NewMqttPublisher(client mqtt.Client) MqttPublisher {
	return &mqttPublisher{client}
}

func NewIoTPublisher() MqttPublisher {
	return &iotPublisher{}
}

func (pub *mqttPublisher) SendCommand(deviceID string, subfolder string, payload []byte) error {
	topic := fmt.Sprintf("/devices/%s/commands/%s", deviceID, subfolder)
	pubtok := pub.client.Publish(topic, qos, retain, payload)
	if !pubtok.WaitTimeout(time.Second * 2) {
		return errors.New("MQTT client timeout")
	}
	return nil
}

func (pub *iotPublisher) SendCommand(deviceID string, subfolder string, payload []byte) error {
	ctx := context.Background()
	client, err := cloudiot.NewService(ctx)
	if err != nil {
		return err
	}

	req := cloudiot.SendCommandToDeviceRequest{
		BinaryData: base64.StdEncoding.EncodeToString(payload),
		Subfolder:  subfolder,
	}

	name := fmt.Sprintf("projects/%s/locations/%s/registries/%s/devices/%s", projectID, region, registryID, deviceID)

	_, err = client.Projects.Locations.Registries.Devices.SendCommandToDevice(name, &req).Do()

	return err
}
