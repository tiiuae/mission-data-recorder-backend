package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/pkg/errors"
	"google.golang.org/api/cloudiot/v1"
	"gopkg.in/yaml.v2"
)

// DroneConfig contains fields to be sent to drone via Cloud IoT Core
type DroneConfig struct {
	Profile             *ProfileConfig     `yaml:"profile"`
	InitialWifi         *InitialWifiConfig `yaml:"initial-wifi"`
	MissionDataRecorder interface{}        `yaml:"mission-data-recorder"`
}

type ProfileConfig struct {
	Version  int    `yaml:"version"`
	URL      string `yaml:"manifest-uri"`
	HashSum  string `yaml:"manifest-hash"`
	SignedBy string `yaml:"signed-by"`
}

type InitialWifiConfig struct {
	ApiVersion int    `yaml:"api_version"`
	SSID       string `yaml:"ssid"`
	Key        string `yaml:"key"`
	Enc        string `yaml:"enc"`
	APMAC      string `yaml:"ap_mac"`
	Country    string `yaml:"country"`
	Frequency  string `yaml:"frequency"`
	IP         string `yaml:"ip"`
	Subnet     string `yaml:"subnet"`
	TXPower    string `yaml:"tx_power"`
	Mode       string `yaml:"mode"`
}

func profileUpdateHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceIDs []string `json:"device_ids"`
		Profile   struct {
			Version  int    `json:"version"`
			URL      string `json:"manifest-uri"`
			HashSum  string `json:"manifest-hash"`
			SignedBy string `json:"signed-by"`
		} `json:"profile"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Could not unmarshal request", err, http.StatusBadRequest)
		return
	}

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Could not create IoT client", err, http.StatusInternalServerError)
		return
	}

	parent := fmt.Sprintf("projects/%s/locations/%s/registries/%s", projectID(), "europe-west1", "fleet-registry")
	call := client.Projects.Locations.Registries.Devices.List(parent)
	call.DeviceIds(request.DeviceIDs...)
	call.FieldMask("config")
	devices, err := call.Do()
	if err != nil {
		writeError(w, "Unable to get drones", err, http.StatusInternalServerError)
		return
	}

	for _, drone := range devices.Devices {
		cfg := deserializeConfigOrDefault(drone)

		if cfg.Profile.URL == request.Profile.URL && cfg.Profile.HashSum == request.Profile.HashSum {
			// no need for update - the device has this exact profile already active
			continue
		}

		// update the configuration in IoT Core
		cfg.Profile.Version = request.Profile.Version
		cfg.Profile.URL = request.Profile.URL
		cfg.Profile.HashSum = request.Profile.HashSum
		cfg.Profile.SignedBy = request.Profile.SignedBy

		newCfg, err := serializeConfig(cfg)
		if err != nil {
			writeError(w, "Could not serialize configuration", err, http.StatusInternalServerError)
			return
		}

		modifyRequest := cloudiot.ModifyCloudToDeviceConfigRequest{
			BinaryData:      newCfg,
			VersionToUpdate: drone.Config.Version,
		}
		deviceName := fmt.Sprintf("%s/devices/%s", parent, drone.Id)
		updateCall := client.Projects.Locations.Registries.Devices.ModifyCloudToDeviceConfig(deviceName, &modifyRequest)
		_, err = updateCall.Do()
		if err != nil {
			writeError(w, "Could not update configuration", err, http.StatusInternalServerError)
			return
		}
	}
}

func deserializeConfigOrDefault(device *cloudiot.Device) *DroneConfig {
	config, err := deserializeConfig(device)
	if err != nil {
		log.Printf("Invalid configuration -> using default: %v", err)
		return defaultConfiguration()
	}

	// Fill missing defaults
	if config.Profile == nil {
		config.Profile = defaultProfile()
	}
	if config.InitialWifi == nil {
		config.InitialWifi = defaultInitialWifi()
	}

	return config
}

func deserializeConfig(device *cloudiot.Device) (*DroneConfig, error) {
	if device.Config.BinaryData == "" {
		return nil, errors.New("No configuration")
	}

	var config DroneConfig
	yamlBytes, err := base64.StdEncoding.DecodeString(device.Config.BinaryData)
	if err != nil {
		return nil, errors.WithMessagef(err, "Failed to decode base64 configuration")
	}

	err = yaml.Unmarshal(yamlBytes, &config)
	if err != nil {
		return nil, errors.WithMessagef(err, "Failed to unmarshal yaml configuration")
	}

	return &config, nil
}

func serializeConfig(config *DroneConfig) (string, error) {
	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return "", errors.WithMessagef(err, "Could not marshal configuration")
	}

	return base64.StdEncoding.EncodeToString(configBytes), nil
}

func defaultConfiguration() *DroneConfig {
	return &DroneConfig{
		Profile:             defaultProfile(),
		InitialWifi:         defaultInitialWifi(),
		MissionDataRecorder: nil,
	}
}

func defaultProfile() *ProfileConfig {
	return &ProfileConfig{
		Version:  0,
		URL:      "",
		HashSum:  "",
		SignedBy: "",
	}
}

func defaultInitialWifi() *InitialWifiConfig {
	return &InitialWifiConfig{
		ApiVersion: 1,
		SSID:       "copper",
		Key:        "1234567890",
		Enc:        "wep",
		APMAC:      "00:11:22:33:44:55",
		Country:    "fi",
		Frequency:  "5220",
		IP:         "192.168.1.1",
		Subnet:     "255.255.255.0",
		TXPower:    "30",
		Mode:       "mesh",
	}
}
