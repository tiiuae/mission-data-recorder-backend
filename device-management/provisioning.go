package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/julienschmidt/httprouter"
	"google.golang.org/api/cloudiot/v1"
	"google.golang.org/api/googleapi"
)

type deviceSettings struct {
	DeviceID         string           `json:"device_id"`
	CommlinkSettings commlinkSettings `json:"communication_link"`
	RecorderSettings recorderSettings `json:"mission_data_recorder"`
	RtspSettings     rtspSettings     `json:"rtsp"`
}

type commlinkSettings struct {
	MqttServer   string `json:"mqtt_server"`
	MqttClientID string `json:"mqtt_client_id"`
	MqttAudience string `json:"mqtt_audience"`
}

type recorderSettings struct {
	Audience      string `json:"audience"`
	BackendURL    string `json:"backend_url"`
	Topics        string `json:"topics"`
	DestDir       string `json:"dest_dir"`
	SizeThreshold int    `json:"size_threshold"`
	ExtraArgs     string `json:"extra_args"`
}

type rtspSettings struct {
	RtspServer string `json:"rtsp_server"`
}

type deviceInfo struct {
	DeviceID string `json:"device_id"`
}

func getDeviceSettings(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	deviceID := params.ByName("deviceID")
	tenantID := getTenantID(r)

	res := deviceSettings{
		DeviceID: deviceID,
		CommlinkSettings: commlinkSettings{
			MqttServer:   "ssl://mqtt.googleapis.com:8883",
			MqttClientID: fmt.Sprintf("%s/devices/%s", registryPath(tenantID), deviceID),
			MqttAudience: projectID(),
		},
		RecorderSettings: recorderSettings{
			Audience:      projectID(),
			BackendURL:    recorderBackendUrl(),
			Topics:        "",
			DestDir:       ".",
			SizeThreshold: 10000000,
			ExtraArgs:     "",
		},
		RtspSettings: rtspSettings{
			RtspServer: rtspBackendUrl(),
		},
	}

	writeJSON(w, res)
}

func listDevices(w http.ResponseWriter, r *http.Request) {
	tenantID := getTenantID(r)

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	result, err := listIotDevices(client, tenantID)
	if err != nil {
		writeError(w, "Failed to list devices", err, http.StatusInternalServerError)
		return
	}

	writeJSON(w, result)
}

func getDevice(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	deviceID := params.ByName("deviceID")
	tenantID := getTenantID(r)

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	call := client.Projects.Locations.Registries.Devices.Get(fmt.Sprintf("%s/devices/%s", registryPath(tenantID), deviceID))
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

func deleteDevice(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	deviceID := params.ByName("deviceID")
	tenantID := getTenantID(r)

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	err = deleteIotDevice(client, tenantID, deviceID)
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code == 404 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		writeError(w, "Failed to delete device", err, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func createDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := getTenantID(r)

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

	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Failed create IOT client", err, http.StatusInternalServerError)
		return
	}

	err = createIotDevice(client, tenantID, request.DeviceID, request.Certificate)
	if err != nil {
		writeError(w, "Failed to create device", err, http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func projectPath() string {
	projectID := projectID()
	region := "europe-west1"
	return fmt.Sprintf("projects/%s/locations/%s", projectID, region)
}

func registryPath(tenantID string) string {
	projectID := projectID()
	region := "europe-west1"
	registryID := tenantID
	return fmt.Sprintf("projects/%s/locations/%s/registries/%s", projectID, region, registryID)
}

func projectID() string {
	return getEnvOrDefault("PROJECT_ID", "auto-fleet-mgnt")
}

func getEnvOrDefault(env, defaultValue string) string {
	value := os.Getenv(env)
	if value == "" {
		return defaultValue
	}
	return value
}

func recorderBackendUrl() string {
	return fmt.Sprintf("mission-data-upload.webapi.%s", subdomain())
}

func rtspBackendUrl() string {
	return fmt.Sprintf("rtsps://DroneUser:22f6c4de-6144-4f6c-82ea-8afcdf19f316@video-stream.%s:8555", subdomain())
}

func subdomain() string {
	switch projectID() {
	case "tii-sac-platform-staging":
		return "staging.sacplatform.com"
	case "tii-sac-platform-demo":
		return "demo.sacplatform.com"
	default:
		return "sacplatform.com"
	}
}

func getTenantID(r *http.Request) string {
	tid := r.URL.Query().Get("tid")
	if tid == "" {
		return "fleet-registry"
	}

	return tid
}
