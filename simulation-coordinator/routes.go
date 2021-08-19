package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func registerRoutes(router *httprouter.Router, enableAuth bool) {
	auth := func(h http.Handler) http.Handler {
		if enableAuth {
			h = checkSimulationAccess(h)
		}
		return h
	}
	router.HandlerFunc(http.MethodGet, "/viewers/:viewerClientID", validateViewerClientID)
	router.HandlerFunc(http.MethodPost, "/commands", sendCommandHandler)
	router.HandlerFunc(http.MethodGet, "/simulations", getSimulationsHandler)
	router.HandlerFunc(http.MethodPost, "/simulations", createSimulationHandler)
	router.Handler(http.MethodGet, "/simulations/:simulationName", auth(http.HandlerFunc(getSimulationHandler)))
	router.Handler(http.MethodDelete, "/simulations/:simulationName", auth(http.HandlerFunc(removeSimulationHandler)))
	router.Handler(http.MethodGet, "/simulations/:simulationName/viewer", auth(refreshSimulationExpiry(startViewerHandler)))
	router.HandlerFunc(http.MethodGet, "/simulations/:simulationName/video/*path", droneVideoStreamWebUIHandler)
	router.Handler(http.MethodGet, "/simulations/:simulationName/drones", auth(refreshSimulationExpiry(getDronesHandler)))
	router.Handler(http.MethodPost, "/simulations/:simulationName/drones", auth(refreshSimulationExpiry(addDroneHandler)))
	router.Handler(http.MethodPost, "/simulations/:simulationName/drones/:droneID/command", auth(refreshSimulationExpiry(commandDroneHandler)))
	router.Handler(http.MethodGet, "/simulations/:simulationName/drones/:droneID/logs", auth(http.HandlerFunc(droneLogStreamHandler)))
	router.Handler(http.MethodGet, "/simulations/:simulationName/drones/:droneID/events", auth(http.HandlerFunc(droneEventStreamHandler)))
	router.Handler(http.MethodPost, "/simulations/:simulationName/drones/:droneID/video", auth(http.HandlerFunc(droneVideoStreamHandler)))
	router.Handler(http.MethodGet, "/simulations/:simulationName/drones/:droneID/shell", auth(refreshSimulationExpiry(droneShellHandler)))
	router.Handler(http.MethodGet, "/simulations/:simulationName/missions", auth(refreshSimulationExpiry(getMissionsHandler)))
	router.Handler(http.MethodPost, "/simulations/:simulationName/missions", auth(refreshSimulationExpiry(createMissionHandler)))
	router.Handler(http.MethodDelete, "/simulations/:simulationName/missions/:missionSlug", auth(refreshSimulationExpiry(deleteMissionHandler)))
	router.Handler(http.MethodPost, "/simulations/:simulationName/missions/:missionSlug/drones", auth(refreshSimulationExpiry(assignDroneHandler)))
	router.Handler(http.MethodPost, "/simulations/:simulationName/missions/:missionSlug/backlog", auth(refreshSimulationExpiry(addBacklogItem)))
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

func writeUnauthorized(w http.ResponseWriter, message string, err error) {
	writeError(w, message, err, http.StatusUnauthorized)
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
