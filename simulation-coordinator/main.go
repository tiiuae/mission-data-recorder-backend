package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/julienschmidt/httprouter"
)

type SimulationGPUMode int

const (
	SimulationGPUModeNone SimulationGPUMode = iota
	SimulationGPUModeNvidia
)

var simulationGPUMode SimulationGPUMode

func main() {
	// SIMULATION_GPU_MODE should be on of following:
	// - none (or empty)
	// - nvidia
	switch os.Getenv("SIMULATION_GPU_MODE") {
	case "nvidia":
		simulationGPUMode = SimulationGPUModeNvidia
	}

	router := httprouter.New()
	registerRoutes(router)

	router.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isValidOrigin(r) && r.Header.Get("Access-Control-Request-Method") != "" {
			// Set CORS headers
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Methods", w.Header().Get("Allow"))
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "3600")
		}

		// Adjust status code to 204
		w.WriteHeader(http.StatusNoContent)
	})

	port := "8087"
	log.Printf("Listening on port %s", port)
	err := http.ListenAndServe(":"+port, setCORSHeader(router))
	if err != nil {
		log.Fatal(err)
	}

	return
}

func setCORSHeader(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isValidOrigin(r) {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		}
		handler.ServeHTTP(w, r)
	})
}

func isValidOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return strings.HasSuffix(o, "localhost:8080") || strings.HasSuffix(o, "sacplatform.com")
}
