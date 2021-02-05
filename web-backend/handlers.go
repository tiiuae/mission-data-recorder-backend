package main

import (
	"context"
	"encoding/json"
	"log"
)

// handle location event from drone
// drone has initialized its ssh keys and is ready to be joined
func handleLocationEvent(c context.Context, deviceID string, payload []byte) {
	var telemetry struct {
		GpsData struct {
			//Timestamp          uint64
			//Timestamp_sample   uint64
			Lat          float64
			Lon          float64
			Alt          float32
			AltEllipsoid float32
			DeltaAlt     float32
			//LatLonResetCounter uint8
			//AltResetCounter    uint8
			Eph             float32
			Epv             float32
			TerrainAlt      float32
			TerrainAltValid bool
			DeadReckoning   bool
		}
		//DeviceId  string
		//MessageID string
	}
	err := json.Unmarshal(payload, &telemetry)
	if err != nil {
		log.Printf("Could not unmarshal telemetry message: %v", err)
		return
	}

	log.Printf("GPS %s (%.8f %.8f)", deviceID, telemetry.GpsData.Lat, telemetry.GpsData.Lon)

	// send updates to all listeners
}
