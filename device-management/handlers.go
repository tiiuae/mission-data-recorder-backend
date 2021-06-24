package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"google.golang.org/api/cloudiot/v1"
	"gopkg.in/yaml.v2"
)

// DroneConfig contains fields to be sent to drone via Cloud IoT Core
type DroneConfig struct {
	Profile struct {
		URL     string
		HashSum string
	}
}

func profileUpdateHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceIDs []string `json:"device_ids"`
		Profile   struct {
			URL     string `json:"url"`
			HashSum string `json:"hashsum"`
		}
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		log.Printf("Could not unmarshal telemetry message: %v", err)
		return
	}

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Could not create IoT client", err, http.StatusInternalServerError)
		return
	}

	parent := fmt.Sprintf("projects/%s/locations/%s/registries/%s", "auto-fleet-mgnt", "europe-west1", "fleet-registry")
	call := client.Projects.Locations.Registries.Devices.List(parent)
	call.DeviceIds(request.DeviceIDs...)
	call.FieldMask("config")
	devices, err := call.Do()
	if err != nil {
		writeError(w, "Unable to get drones", err, http.StatusInternalServerError)
		return
	}

	for _, drone := range devices.Devices {
		var cfg DroneConfig
		err := yaml.Unmarshal([]byte(drone.Config.BinaryData), &cfg)
		if err != nil {
			log.Fatalf("Could not unmarshal configuration: %v", err)
		}
		if cfg.Profile.URL == request.Profile.URL && cfg.Profile.HashSum == request.Profile.HashSum {
			// no need for update - the device has this exact profile already active
			continue
		}

		// update the configuration in IoT Core
		cfg.Profile.URL = request.Profile.URL
		cfg.Profile.HashSum = request.Profile.HashSum
		configBytes, err := yaml.Marshal(cfg)
		if err != nil {
			log.Fatalf("Could not marshal configuration: %v", err)
		}
		configBytesBase64 := base64.StdEncoding.EncodeToString(configBytes)
		modifyRequest := cloudiot.ModifyCloudToDeviceConfigRequest{
			BinaryData:      string(configBytesBase64),
			VersionToUpdate: drone.Config.Version,
		}
		deviceName := fmt.Sprintf("%s/devices/%s", parent, drone.Id)
		updateCall := client.Projects.Locations.Registries.Devices.ModifyCloudToDeviceConfig(deviceName, &modifyRequest)
		_, err = updateCall.Do()
		if err != nil {
			log.Fatalf("Could not update configuration: %v", err)
		}
	}
}
