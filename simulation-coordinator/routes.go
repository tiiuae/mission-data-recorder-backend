package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func registerRoutes(router *httprouter.Router) {
	router.HandlerFunc(http.MethodGet, "/viewers/:viewerClientID", validateViewerClientID)
	router.HandlerFunc(http.MethodGet, "/simulations", getSimulationsHandler)
	router.HandlerFunc(http.MethodPost, "/simulations", createSimulationHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName", getSimulationHandler)
	router.HandlerFunc(http.MethodDelete, "/simulations/:simulationName", removeSimulationHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/viewer", startViewerHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/drones", getDronesHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/drones", addDroneHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/drones/:droneID/command", commandDroneHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/drones/:droneID/logs", droneLogStreamHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/drones/:droneID/events", droneEventStreamHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/drones/:droneID/video", droneVideoStreamHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/drones/:droneID/shell", droneShellHandler)
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/missions", getMissionsHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/missions", createMissionHandler)
	router.HandlerFunc(http.MethodDelete, "/simulations/:simulationName/missions/:missionSlug", deleteMissionHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/missions/:missionSlug/drones", assignDroneHandler)
	router.HandlerFunc(http.MethodPost, "/simulations/:simulationName/missions/:missionSlug/backlog", addBacklogItem)
	router.HandlerFunc(http.MethodGet, "/events/:droneID/*path", eventsHandler)
	router.HandlerFunc(http.MethodGet, "/healthz", healthz)
}

func writeJSONWithCode(w http.ResponseWriter, code int, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		writeServerError(w, "Could not marshal data to json", err)
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
	text := message
	if err != nil {
		text = fmt.Sprintf("%s: %v", message, err)
	}
	log.Println(text)
	writeJSONWithCode(w, code, obj{"error": text})
}

func writeServerError(w http.ResponseWriter, message string, err error) {
	writeError(w, message, err, http.StatusInternalServerError)
}

func writeBadRequest(w http.ResponseWriter, message string, err error) {
	writeError(w, message, err, http.StatusBadRequest)
}

func writeNotFound(w http.ResponseWriter, message string, err error) {
	writeError(w, message, err, http.StatusNotFound)
}

func writeInvalidJSON(w http.ResponseWriter, err error) {
	writeBadRequest(w, "request body must be valid JSON", err)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
