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

	"github.com/julienschmidt/httprouter"
	"google.golang.org/api/cloudiot/v1"
	"nhooyr.io/websocket"
)

var armingStateMap = map[uint8]string{
	0: "Init",
	1: "Standby",
	2: "Armed",
	3: "Standby error",
	4: "Shutdown",
	5: "In air restore",
}
var navigationModeMap = map[uint8]string{
	0:  "Manual mode",
	1:  "Altitude control mode",
	2:  "Position control mode",
	3:  "Auto mission mode",
	4:  "Auto loiter mode",
	5:  "Auto return to launch mode",
	8:  "Auto land on engine failure",
	9:  "Auto land on gps failure",
	10: "Acro mode",
	12: "Descend mode",
	13: "Termination mode",
	14: "Offboard mode",
	15: "Stabilized mode",
	16: "Rattitude (aka \"flip\") mode",
	17: "Takeoff mode",
	18: "Land mode",
	19: "Follow mode",
	20: "Precision land with landing target",
	21: "Orbit mode",
}

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

func debugStartMission(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	droneID := params.ByName("droneID")

	msg, err := json.Marshal(struct {
		Command string
		Payload string
	}{
		Command: "start_mission",
		Payload: "",
	})

	if err != nil {
		log.Printf("Could not marshal join-mission command: %v\n", err)
		return
	}

	log.Printf("Sending start_mission command to %s", droneID)

	err = mqttPub.SendCommand(droneID, "control", msg)
	if err != nil {
		log.Printf("Could not publish message to MQTT broker: %v", err)
		return
	}
}

func subscribeWebsocket(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	// accept websocket
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:8080", "sacplatform.com"},
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
	case "telemetry":
		go handleTelemetryEvent(context.Background(), deviceID, payload)
	case "debug-values":
		go handleDebugEvent(context.Background(), deviceID, payload)
	}
}

type websocketEvent struct {
	Event   string      `json:"event"`
	Device  string      `json:"device"`
	Payload interface{} `json:"payload"`
}

type telemetryEvent struct {
	LocationUpdated  bool    `json:"location_updated"`
	Lat              float64 `json:"lat"`
	Lon              float64 `json:"lon"`
	Heading          float32 `json:"heading"`
	AltitudeFromHome float32 `json:"altitude_from_home"`
	DistanceFromHome float32 `json:"distance_from_home"`

	BatteryUpdated   bool    `json:"battery_updated"`
	BatteryVoltage   float32 `json:"battery_voltage"`
	BatteryRemaining float32 `json:"battery_remaining"`

	StateUpdated   bool   `json:"state_updated"`
	ArmingState    string `json:"arming_state"`
	NavigationMode string `json:"navigation_mode"`
}

type debugValueEvent struct {
	Key     string    `json:"key"`
	Value   string    `json:"value"`
	Updated time.Time `json:"updated"`
}

func handleTelemetryEvent(c context.Context, deviceID string, payload []byte) {
	var telemetry struct {
		Timestamp int64
		MessageID string

		LocationUpdated  bool
		Lat              float64
		Lon              float64
		Heading          float32
		AltitudeFromHome float32
		DistanceFromHome float32

		BatteryUpdated   bool
		BatteryVoltageV  float32
		BatteryRemaining float32

		StateUpdated bool
		ArmingState  uint8
		NavState     uint8
	}
	err := json.Unmarshal(payload, &telemetry)
	if err != nil {
		log.Printf("Could not unmarshal telemetry message: %v", err)
		return
	}

	//log.Printf("GPS %s (%.8f %.8f)", deviceID, telemetry.GpsData.Lat, telemetry.GpsData.Lon)

	armingState, ok := armingStateMap[telemetry.ArmingState]
	if !ok {
		armingState = "UNKNOWN STATE"
	}
	navMode, ok := navigationModeMap[telemetry.NavState]
	if !ok {
		navMode = "UNKNOWN MODE"
	}

	msg, _ := json.Marshal(websocketEvent{
		Event:  "telemetry",
		Device: deviceID,
		Payload: telemetryEvent{
			LocationUpdated:  telemetry.LocationUpdated,
			Lat:              telemetry.Lat,
			Lon:              telemetry.Lon,
			Heading:          telemetry.Heading,
			AltitudeFromHome: telemetry.AltitudeFromHome,
			DistanceFromHome: telemetry.DistanceFromHome,

			BatteryUpdated:   telemetry.BatteryUpdated,
			BatteryVoltage:   telemetry.BatteryVoltageV,
			BatteryRemaining: telemetry.BatteryRemaining,

			StateUpdated:   telemetry.StateUpdated,
			ArmingState:    armingState,
			NavigationMode: navMode,
		},
	})

	go publishMessage(msg)
}

type debugValue struct {
	Updated time.Time `json:"updated"`
	Value   string    `json:"value"`
}

type debugValues map[string]debugValue

func handleDebugEvent(c context.Context, deviceID string, payload []byte) {
	var dv debugValues
	err := json.Unmarshal(payload, &dv)
	if err != nil {
		log.Printf("Could not unmarshal debug-value message: %v", err)
		return
	}

	msg, _ := json.Marshal(websocketEvent{
		Event:   "debug-values",
		Device:  deviceID,
		Payload: dv,
	})
	go publishMessage(msg)

	go func() {

		for k, v := range dv {
			msg, _ := json.Marshal(websocketEvent{
				Event:  "debug-value",
				Device: deviceID,
				Payload: debugValueEvent{
					Key:     k,
					Value:   v.Value,
					Updated: v.Updated,
				},
			})

			publishMessage(msg)
		}
	}()
}
