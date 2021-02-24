package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"

	"google.golang.org/api/cloudiot/v1"
	"nhooyr.io/websocket"
)

type subscriber struct {
	messages        chan []byte
	closeConnection func()
}

var (
	subscribersMu sync.Mutex
	subscribers   map[*subscriber]struct{} = make(map[*subscriber]struct{})
)

func getDronesMinikube(w http.ResponseWriter, r *http.Request) {
	// call gzserver to get simulation drones
	resp, err := http.Get("http://gzserver-api-svc:8081/simulation/drones")
	if err != nil {
		writeError(w, "Unable to list drones", err, http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := ioutil.ReadAll(resp.Body)
		writeError(w, fmt.Sprintf("Unable to list drones: (%d): %v", resp.StatusCode, string(msg)), nil, http.StatusInternalServerError)
		return
	}

	var gzserverResponse []struct {
		DeviceID      string `json:"device_id"`
		DroneLocation string `json:"drone_location"`
	}
	err = json.NewDecoder(resp.Body).Decode(&gzserverResponse)
	if err != nil {
		writeError(w, "Response was not formatted correctly", err, http.StatusInternalServerError)
		return
	}

	var response []string
	for _, drone := range gzserverResponse {
		response = append(response, drone.DeviceID)
	}
	writeJSON(w, response)
}

func getDronesCloud(w http.ResponseWriter, r *http.Request) {
	client, err := cloudiot.NewService(r.Context())
	if err != nil {
		writeError(w, "Could not create IoT client", err, http.StatusInternalServerError)
		return
	}

	parent := fmt.Sprintf("projects/%s/locations/%s/registries/%s", "auto-fleet-mgnt", "europe-west1", "fleet-registry")
	call := client.Projects.Locations.Registries.Devices.List(parent)
	call.FieldMask("lastHeartbeatTime,lastEventTime,lastStateTime,metadata")
	devices, err := call.Do()
	if err != nil {
		writeError(w, "Unable to list drones", err, http.StatusInternalServerError)
		return
	}

	var response []string
	for _, drone := range devices.Devices {
		response = append(response, drone.Id)
	}
	writeJSON(w, response)
}

func subscribeWebsocket(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	// accept websocket
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:8080"},
	})
	if err != nil {
		log.Printf("Unable to accept websocket: %v", err)
		return
	}
	defer conn.Close(websocket.StatusInternalError, "")

	// create subscriber
	s := subscriber{
		messages: make(chan []byte, 32), // buffer of 32 messages
		closeConnection: func() {
			conn.Close(websocket.StatusPolicyViolation, "connection too slow to keep up with messages")
		},
	}
	addSubscriber(&s)
	defer removeSubscriber(&s)

	// publish messages
	for {
		select {
		case <-c.Done():
			log.Printf("Context done: %v", c.Err())
			return
		case msg := <-s.messages:
			err = writeTimeout(c, 2*time.Second, conn, msg)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
					websocket.CloseStatus(err) == websocket.StatusGoingAway {
					return
				}
				log.Printf("Write to websocket failed: %v", err)
				return
			}
		}
	}
}

func writeTimeout(c context.Context, timeout time.Duration, conn *websocket.Conn, msg []byte) error {
	c, cancel := context.WithTimeout(c, timeout)
	defer cancel()

	return conn.Write(c, websocket.MessageText, msg)
}

func addSubscriber(s *subscriber) {
	subscribersMu.Lock()
	subscribers[s] = struct{}{}
	subscribersMu.Unlock()
}
func removeSubscriber(s *subscriber) {
	subscribersMu.Lock()
	delete(subscribers, s)
	subscribersMu.Unlock()
}

func publishMessage(message []byte) {
	subscribersMu.Lock()
	defer subscribersMu.Unlock()
	for s := range subscribers {
		select {
		case s.messages <- message:
		default:
			// buffer for this subscriber is full
			s.closeConnection()
		}
	}
}

func handleMQTTEvent(deviceID string, topic string, payload []byte) {
	log.Printf("Event: %s %s\n", deviceID, topic)
	switch topic {
	case "location":
		go handleLocationEvent(context.Background(), deviceID, payload)
	}
}

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

	//log.Printf("GPS %s (%.8f %.8f)", deviceID, telemetry.GpsData.Lat, telemetry.GpsData.Lon)

	msg, _ := json.Marshal(struct {
		Device string  `json:"device"`
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
	}{
		Device: deviceID,
		Lat:    telemetry.GpsData.Lat,
		Lon:    telemetry.GpsData.Lon,
	})
	// send updates to all listeners
	go publishMessage(msg)
}
