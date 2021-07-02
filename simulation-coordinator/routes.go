package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func registerRoutes(router *httprouter.Router) {
	router.HandlerFunc(http.MethodGet, "/simulations", getSimulationsHandler)
	router.HandlerFunc(http.MethodPost, "/simulations", createSimulationHandler)
	router.HandlerFunc(http.MethodDelete, "/simulations/:simulationName", removeSimulationHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/viewer", startViewerHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/drones", addDroneHandler)
	router.HandlerFunc(http.MethodGet, "/healthz", healthz)
}

func writeJSONWithCode(w http.ResponseWriter, code int, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		writeError(w, "Could not marshal data to json", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	writeJSONWithCode(w, 200, data)
}

func writeError(w http.ResponseWriter, message string, err error, code int) {
	text := fmt.Sprintf("%s: %v", message, err)
	log.Println(text)
	writeJSONWithCode(w, code, map[string]string{"error": text})
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
