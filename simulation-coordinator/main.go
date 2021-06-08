package main

import (
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"

	v1 "github.com/tiiuae/fleet-management/simulation-coordinator/pkg/controller/v1"
	"github.com/tiiuae/fleet-management/simulation-coordinator/pkg/cors"
)

func main() {
	router := httprouter.New()
	registerRoutes(router)
	cors.HandleCORS(router)

	port := "8087"
	log.Printf("Listening on port %s", port)
	err := http.ListenAndServe(":"+port, router)
	if err != nil {
		log.Fatal(err)
	}
}

func registerRoutes(router *httprouter.Router) {
	router.GET("/v1/simulations", v1.GetSimulationsHandler)
	router.POST("/v1/simulations", v1.CreateSimulationHandler)
	router.GET("/healthz", v1.GETHealthz)
}
