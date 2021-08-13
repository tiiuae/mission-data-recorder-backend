package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func registerRoutes(router *httprouter.Router) {
	router.HandlerFunc(http.MethodGet, "/healthz", healthz)

	// OTA
	router.HandlerFunc(http.MethodPost, "/profile-update", profileUpdateHandler)

	// Provisioning
	router.HandlerFunc(http.MethodGet, "/device-settings/:deviceID", getDeviceSettings)
	router.HandlerFunc(http.MethodPost, "/devices", createDevice)
	router.HandlerFunc(http.MethodGet, "/devices", listDevices)
	router.HandlerFunc(http.MethodGet, "/devices/:deviceID", getDevice)
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Printf("Could not marshal data to json: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func writeError(w http.ResponseWriter, message string, err error, code int) {
	text := fmt.Sprintf("%s: %v", message, err)
	log.Println(text)
	http.Error(w, text, code)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
