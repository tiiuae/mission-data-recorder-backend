package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"
	"google.golang.org/api/cloudiot/v1"
	"google.golang.org/api/googleapi"
)

func createTenant(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	tenantID := params.ByName("tenantID")

	client, err := cloudiot.NewService(c)
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	projectID := projectID()
	parent := projectPath()

	newRegistry := &cloudiot.DeviceRegistry{
		Id: tenantID,
		HttpConfig: &cloudiot.HttpConfig{
			HttpEnabledState: "HTTP_DISABLED",
		},
		EventNotificationConfigs: []*cloudiot.EventNotificationConfig{
			{
				PubsubTopicName:  fmt.Sprintf("projects/%s/topics/iot-device-imu", projectID),
				SubfolderMatches: "sensordata",
			},
			{
				PubsubTopicName:  fmt.Sprintf("projects/%s/topics/iot-device-location", projectID),
				SubfolderMatches: "location",
			},
			{
				PubsubTopicName:  fmt.Sprintf("projects/%s/topics/iot-device-telemetry", projectID),
				SubfolderMatches: "telemetry",
			},
			{
				PubsubTopicName:  fmt.Sprintf("projects/%s/topics/iot-device-debug-values", projectID),
				SubfolderMatches: "debug-values",
			},
			{
				PubsubTopicName:  fmt.Sprintf("projects/%s/topics/iot-device-debug-events", projectID),
				SubfolderMatches: "events",
			},
			{
				PubsubTopicName: fmt.Sprintf("projects/%s/topics/machine-telemetry", projectID),
			},
		},
	}
	call := client.Projects.Locations.Registries.Create(parent, newRegistry)
	_, err = call.Do()

	if err != nil {
		writeError(w, "Failed to create device registry", err, http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func deleteTenant(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	tenantID := params.ByName("tenantID")

	if !strings.Contains(tenantID, "~s~") {
		log.Printf("Can't delete tenant '%s' (not simulation)", tenantID)
		http.Error(w, "", http.StatusForbidden)
		return
	}

	client, err := cloudiot.NewService(c)
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	devices, err := listIotDevices(client, tenantID)

	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		writeError(w, "Failed to list devices", err, http.StatusInternalServerError)
		return
	}

	for _, device := range devices {
		log.Printf("Deleting tenant '%s' / device '%s'", tenantID, device.DeviceID)
		err = deleteIotDevice(client, tenantID, device.DeviceID)
		if err != nil {
			writeError(w, "Failed to remove device", err, http.StatusInternalServerError)
			return
		}
	}

	parent := registryPath(tenantID)

	call := client.Projects.Locations.Registries.Delete(parent)
	_, err = call.Do()
	if err != nil {
		writeError(w, "Failed to delete registry", err, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
