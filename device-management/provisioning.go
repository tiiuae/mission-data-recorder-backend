package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"google.golang.org/api/cloudiot/v1"
	"google.golang.org/api/googleapi"
)

type deviceSettings struct {
	DeviceID     string `json:"device_id"`
	MqttServer   string `json:"mqtt_server"`
	MqttClientID string `json:"mqtt_client_id"`
	MqttAudience string `json:"mqtt_audience"`
}

type deviceInfo struct {
	DeviceID string `json:"device_id"`
}

func getDeviceSettings(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	deviceID := params.ByName("deviceID")

	res := deviceSettings{
		DeviceID:     deviceID,
		MqttServer:   "ssl://mqtt.googleapis.com:8883",
		MqttClientID: fmt.Sprintf("%s/devices/%s", registryPath(), deviceID),
		MqttAudience: projectID(),
	}

	writeJSON(w, res)
}

func listDevices(w http.ResponseWriter, r *http.Request) {
	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	call := client.Projects.Locations.Registries.Devices.List(registryPath())
	call.FieldMask("lastHeartbeatTime,lastEventTime,lastStateTime,metadata")
	devices, err := call.Do()
	if err != nil {
		writeError(w, "Failed create list devices", err, http.StatusInternalServerError)
		return
	}

	result := make([]deviceInfo, 0)

	for _, device := range devices.Devices {
		result = append(result, deviceInfo{device.Id})
	}

	writeJSON(w, result)
}

func getDevice(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	deviceID := params.ByName("deviceID")

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	call := client.Projects.Locations.Registries.Devices.Get(fmt.Sprintf("%s/devices/%s", registryPath(), deviceID))
	device, err := call.Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		writeError(w, "Failed to get device info", err, http.StatusInternalServerError)
		return
	}

	writeJSON(w, deviceInfo{device.Id})
}

func createDevice(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceID    string `json:"device_id"`
		Certificate []byte `json:"certificate"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Failed to unmarshal request", err, http.StatusBadRequest)
		return
	}

	err = createIoTDevice(r.Context(), request.DeviceID, request.Certificate)
	if err != nil {
		writeError(w, "Failed to create device", err, http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func createIoTDevice(ctx context.Context, deviceID string, certificate []byte) error {
	client, err := cloudiot.NewService(ctx)
	if err != nil {
		return err
	}

	newDevice := &cloudiot.Device{
		Blocked: false,
		Config:  nil,
		Credentials: []*cloudiot.DeviceCredential{
			{
				ExpirationTime: "",
				PublicKey: &cloudiot.PublicKeyCredential{
					Format: "RSA_X509_PEM",
					Key:    string(certificate),
				},
			},
		},
		GatewayConfig: nil,
		Id:            deviceID,
	}

	call := client.Projects.Locations.Registries.Devices.Create(registryPath(), newDevice)
	_, err = call.Do()

	return err
}

func registryPath() string {
	projectID := projectID()
	region := "europe-west1"
	registryID := "fleet-registry"
	return fmt.Sprintf("projects/%s/locations/%s/registries/%s", projectID, region, registryID)
}

func projectID() string {
	return "auto-fleet-mgnt"
}
