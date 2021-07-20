package main

import (
	"bufio"
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/tiiuae/fleet-management/simulation-coordinator/kube"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

var (
	imageGPUTag = map[SimulationGPUMode]string{
		SimulationGPUModeNone:   ":latest",
		SimulationGPUModeNvidia: ":nvidia",
	}
)

var websocketUpgrader websocket.Upgrader

var errSimDoesNotExist = errors.New("simulation doesn't exist")

func getSimulationType(ctx context.Context, simulationName string) (kube.SimulationType, error) {
	ns, err := getKube().CoreV1().Namespaces().Get(ctx, simulationName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return "", errSimDoesNotExist
	} else if err != nil {
		return "", err
	}
	simType := ns.Labels["dronsole-simulation-type"]
	switch kube.SimulationType(simType) {
	case kube.SimulationGlobal:
	case kube.SimulationStandalone:
	case "":
		return "", errSimDoesNotExist
	default:
		return "", errors.New("invalid simulation type: " + simType)
	}
	return kube.SimulationType(simType), nil
}

// GET /simulations
func getSimulationsHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	kube := getKube()
	namespaces, err := kube.CoreV1().Namespaces().List(c, metav1.ListOptions{LabelSelector: "dronsole-type=simulation"})
	if err != nil {
		writeError(w, "Could not get simulations", err, http.StatusInternalServerError)
		return
	}

	type resp struct {
		Name      string    `json:"name"`
		Phase     string    `json:"phase"`
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	response := make([]resp, len(namespaces.Items))
	for i, ns := range namespaces.Items {
		expires, _ := time.Parse(time.RFC3339, ns.Annotations["dronsole-expiration-timestamp"])
		response[i] = resp{
			Name:      ns.Name,
			Phase:     fmt.Sprintf("%s", ns.Status.Phase),
			Type:      ns.Labels["dronsole-simulation-type"],
			CreatedAt: ns.CreationTimestamp.Time,
			ExpiresAt: expires,
		}
	}
	writeJSON(w, obj{"simulations": response})
}

func getSimulationHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")

	simType, err := getSimulationType(r.Context(), simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	if simType == kube.SimulationGlobal {
		writeJSON(w, obj{"mqtt_server": obj{"url": mqttServerURL}})
		return
	}
	mqttIP, err := waitLoadBalancerIP(r.Context(), simulationName, "mqtt-server-svc")
	if err != nil {
		writeServerError(w, "Error getting mqtt server address", err)
		return
	}
	writeJSON(w, obj{"mqtt_server": obj{"url": "tcp://" + mqttIP + ":8883"}})
}

func kubeSimGZServerDeployment(dataImage string) *appsv1.Deployment {
	// Volume definitions
	volumeGazeboData := v1.Volume{
		Name: "gazebo-data-vol",
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
		},
	}
	hostPathType := v1.HostPathDirectoryOrCreate
	volumeXSOCK := v1.Volume{
		Name: "xsock",
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{
				Path: "/tmp/.X11-unix",
				Type: &hostPathType,
			},
		},
	}
	volumeXAUTH := v1.Volume{
		Name: "xauth",
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{
				Path: "/tmp/.docker.xauth",
				Type: &hostPathType,
			},
		},
	}

	volumeMountGazeboData := v1.VolumeMount{
		MountPath: "/data",
		Name:      "gazebo-data-vol",
	}
	volumeMountXSOCK := v1.VolumeMount{
		Name:      "xsock",
		MountPath: "/tmp/.X11-unix",
	}
	volumeMountXAUTH := v1.VolumeMount{
		Name:      "xauth",
		MountPath: "/tmp/.docker.xauth",
	}

	volumes := []v1.Volume{
		volumeGazeboData,
	}
	volumeMounts := []v1.VolumeMount{
		volumeMountGazeboData,
	}
	env := []v1.EnvVar{}
	if simulationGPUMode != SimulationGPUModeNone {
		// GPU acceleration needs X server resources from the host machine
		// will mount these to the gzserver
		volumes = append(volumes, volumeXSOCK, volumeXAUTH)
		volumeMounts = append(volumeMounts, volumeMountXSOCK, volumeMountXAUTH)
		env = append(env, v1.EnvVar{
			Name:  "DISPLAY",
			Value: os.Getenv("DISPLAY"),
		})
		env = append(env, v1.EnvVar{
			Name:  "XAUTHORITY",
			Value: "/tmp/.docker.xauth",
		})
	}

	gzserverDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gzserver-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "gzserver-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "gzserver-pod",
					},
				},
				Spec: v1.PodSpec{
					Volumes: volumes,
					InitContainers: []v1.Container{
						{
							Name:            "gazebo-data",
							Image:           dataImage,
							ImagePullPolicy: v1.PullAlways,
							Command:         []string{"cp", "-r", "/gazebo-data/models", "/gazebo-data/worlds", "/gazebo-data/scripts", "/gazebo-data/plugins", "/data"},
							VolumeMounts: []v1.VolumeMount{
								volumeMountGazeboData,
							},
						},
					},
					Containers: []v1.Container{
						{
							Name:            "gzserver",
							Image:           imageGZServer + imageGPUTag[simulationGPUMode],
							ImagePullPolicy: v1.PullAlways,
							Env:             env,
							VolumeMounts:    volumeMounts,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}
	return gzserverDeployment
}
func kubeSimGZServerService() *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gzserver-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name: "gazebo",
					Port: 11345,
				},
				{
					Name: "api",
					Port: 8081,
				},
			},

			Selector: map[string]string{"app": "gzserver-pod"},
			Type:     v1.ServiceTypeClusterIP,
		},
	}
}

// POST /simulations
func createSimulationHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	var request struct {
		Name       string `json:"name"`
		World      string `json:"world"`
		Standalone bool   `json:"standalone"`
		DataImage  string `json:"data_image"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Could not unmarshal simulation request", err, http.StatusInternalServerError)
		return
	}

	clientset := getKube()
	if request.Name == "" {
		request.Name = generateSimulationName(c, clientset)
	}
	log.Printf("Creating simulation %s with world %s", request.Name, request.World)

	simType := kube.SimulationGlobal
	if request.Standalone {
		simType = kube.SimulationStandalone
	}
	ns, err := kube.CreateNamespace(c, request.Name, simType, clientset)
	if err != nil {
		writeError(w, "Could not create namespace for the simulation", err, http.StatusInternalServerError)
		return
	}
	err = kube.CopySecret(c, currentNamespace, dockerConfigSecretName, ns.Name, "dockerconfigjson", clientset)
	if err != nil {
		writeServerError(w, "Could not copy Docker configuration for the simulation", err)
		return
	}

	gzserverDeployment := kubeSimGZServerDeployment(request.DataImage)
	gzserverDeployment, err = clientset.AppsV1().Deployments(request.Name).Create(c, gzserverDeployment, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create gzserver deployment", err, http.StatusInternalServerError)
		err = clientset.CoreV1().Namespaces().Delete(c, request.Name, *metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after gzserver deployment creation failed: %v", err))
		}
		return
	}

	gzserverService := kubeSimGZServerService()
	gzserverService, err = clientset.CoreV1().Services(request.Name).Create(c, gzserverService, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create gzserver service", err, http.StatusInternalServerError)
		err = clientset.CoreV1().Namespaces().Delete(c, request.Name, *metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after gzserver service creation failed: %v", err))
		}
		return
	}

	if request.Standalone {
		err = kube.CreateMQTT(c, request.Name, imageMQTTServer, clientset)
		if err != nil {
			writeServerError(w, "error creating mqtt-server deployment: %w", err)
			if err != nil {
				panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
			}
			return
		}
		err = kube.CreateMissionControl(c, request.Name, imageMissionControl, clientset)
		if err != nil {
			writeServerError(w, "error creating mission-control deployment: %w", err)
			if err != nil {
				panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
			}
			return
		}
		err = kube.CreateVideoServer(c, request.Name, imageVideoServer, clientset)
		if err != nil {
			writeServerError(w, "error creating video-server deployment: %w", err)
			if err != nil {
				panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
			}
			return
		}
		err = kube.CreateVideoMultiplexer(c, request.Name, imageVideoMultiplexer, clientset)
		if err != nil {
			writeServerError(w, "error creating video-multiplexer deployment: %w", err)
			if err != nil {
				panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
			}
			return
		}
		err = kube.CreateWebBackend(c, request.Name, imageWebBackend, clientset)
		if err != nil {
			writeServerError(w, "error creating web-backend deployment: %w", err)
			if err != nil {
				panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
			}
			return
		}
	}

	// request world creation
	requestBody, err := json.Marshal(obj{
		"world_file": request.World,
	})
	if err != nil {
		panic("Could not marshal body")
	}

	if err = waitDeploymentAvailable(c, request.Name, "gzserver-dep"); err != nil {
		writeServerError(w, "failed to wait for gzserver", err)
		return
	}

	// start the simulation by calling the service
	startURL := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/start", request.Name)
	// retry max 32 times
	for i := 0; i < 32; i++ {
		_, err = http.Post(startURL, "application/json", bytes.NewBuffer(requestBody))
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		writeError(w, "Could not start simulation on gzserver", err, http.StatusInternalServerError)
		err = clientset.CoreV1().Namespaces().Delete(c, request.Name, *metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
		}
		return
	}

	log.Printf("Simulation started")
	writeJSON(w, obj{"name": request.Name})
}

func removeSimulationHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	kube := getKube()

	err := kube.CoreV1().Namespaces().Delete(c, simulationName, *metav1.NewDeleteOptions(10))
	if err != nil {
		writeError(w, "Could not delete simulation", err, http.StatusInternalServerError)
		return
	}
}

var viewerClients sync.Map // map[string]nil

func startViewerHandler(w http.ResponseWriter, r *http.Request) {
	c, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	err := kube.CreateViewer(
		c,
		simulationName,
		imageGZWeb+":latest",
		"http://simulation-coordinator-svc."+currentNamespace,
		getKube(),
	)
	if err != nil {
		writeServerError(w, "Unable to create viewer", err)
		return
	}
	var viewerClientID string
	for {
		viewerClientID = uuid.New().String()
		_, loaded := viewerClients.LoadOrStore(viewerClientID, nil)
		if !loaded {
			defer viewerClients.Delete(viewerClientID)
			break
		}
	}
	conn, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		writeBadRequest(w, "failed to start websocket connection", err)
		return
	}
	defer conn.Close()
	ip, err := waitLoadBalancerIP(c, simulationName, "gzweb-svc")
	resp := obj{
		"id":   viewerClientID,
		"host": ip,
	}
	if err = conn.WriteJSON(resp); err != nil {
		log.Println(err)
		return
	}
	go func() {
		defer func() {
			cancel()
			conn.Close()
		}()
		for {
			if _, _, err := conn.NextReader(); err != nil {
				log.Println(err)
				return
			}
		}
	}()
	<-c.Done()
	log.Println("viewer client closed")
}

func validateViewerClientID(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	if _, ok := viewerClients.Load(params.ByName("viewerClientID")); !ok {
		writeError(w, "forbidden", nil, http.StatusForbidden)
	}
}

func addDroneHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	var request struct {
		DroneID    string  `json:"drone_id"`
		PrivateKey string  `json:"private_key"` // ignored if Location != "cluster"
		PosX       float64 `json:"pos_x"`
		PosY       float64 `json:"pos_y"`
		PosZ       float64 `json:"pos_z"`
		Yaw        float64 `json:"yaw"`
		Pitch      float64 `json:"pitch"`
		Roll       float64 `json:"roll"`
		Location   string  `json:"location"` // "cluster", "local" or "remote"

		// the following are ignored if Location == "cluster"
		MAVLinkAddress string `json:"mavlink_address"`
		MAVLinkUDPPort int    `json:"mavlink_udp_port"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Could not unmarshal simulation request", err, http.StatusInternalServerError)
		return
	}
	switch request.Location {
	case "cluster":
		simType, err := getSimulationType(c, simulationName)
		if errors.Is(err, errSimDoesNotExist) {
			writeNotFound(w, "simulation doesn't exist", nil)
			return
		} else if err != nil {
			writeServerError(w, "failed to add drone", err)
			return
		}
		opts := &kube.CreateDroneOptions{
			Image:     imageFogDrone,
			Namespace: simulationName,
		}
		switch simType {
		case kube.SimulationGlobal:
			if request.PrivateKey == "" {
				writeBadRequest(w, "identity key is required for non-standalone simulations", nil)
				return
			}
			if request.DroneID == "" {
				writeBadRequest(w, "drone ID is required for non-standalone simulations", nil)
				return
			}
			opts.MQTTBrokerAddress = mqttServerURL
			opts.RTSPServerAddress = videoServerUsername + ":" + videoServerPassword + "@" + videoServerHost
		case kube.SimulationStandalone:
			if request.PrivateKey == "" {
				request.PrivateKey, _, err = generateMQTTCertificate()
				if err != nil {
					writeBadRequest(w, "Automatically generation of private key failed. Provide it in the request body.", nil)
					return
				}
			}
			if request.DroneID == "" {
				request.DroneID, err = generateDroneID(c, simulationName)
				if err != nil {
					writeBadRequest(w, "Automatic generation of drone ID failed. Provide a unique id in the request body.", err)
					return
				}
			}
			opts.MQTTBrokerAddress = "tcp://mqtt-server-svc:8883"
			opts.RTSPServerAddress = videoServerUsername + ":" + videoServerPassword + "@video-server-svc:8554"
		default:
			panic("invalid simulation type: " + simType)
		}
		opts.DeviceID = request.DroneID
		opts.PrivateKey = request.PrivateKey
		err = kube.CreateDrone(c, getKube(), opts)
		if err != nil {
			writeError(w, "Could not create drone deployment", err, http.StatusInternalServerError)
			return
		}
		request.MAVLinkAddress = fmt.Sprintf("drone-%s-svc", request.DroneID)
		request.MAVLinkUDPPort = 14560
	case "local", "remote":
	default:
		writeBadRequest(w, `location must be one of "cluster", "local" or "remote"`, nil)
		return
	}
	requestBody, err := json.Marshal(obj{
		"drone_location":   request.Location,
		"device_id":        request.DroneID,
		"mavlink_address":  request.MAVLinkAddress,
		"mavlink_udp_port": request.MAVLinkUDPPort,
		"video_udp_port":   5600,
		"pos_x":            request.PosX,
		"pos_y":            request.PosY,
		"pos_z":            request.PosZ,
		"yaw":              request.Yaw,
		"pitch":            request.Pitch,
		"roll":             request.Roll,
	})
	if err != nil {
		panic(err)
	}

	droneURL := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/drones", simulationName)
	resp, err := http.Post(droneURL, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		writeError(w, "Could not add drone to gzserver", err, http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := ioutil.ReadAll(resp.Body)
		writeError(w, fmt.Sprintf("Could not add drone to gzserver (%d): %v", resp.StatusCode, string(msg)), nil, http.StatusInternalServerError)
		return
	}
	writeJSON(w, obj{"drone_id": request.DroneID})
}

func generateMQTTCertificate() (privateKey, publicKey string, err error) {
	priv, err := rsa.GenerateKey(cryptoRand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("could not generate private rsa key: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "unused",
		},
	}
	cert, err := x509.CreateCertificate(cryptoRand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("could not generate certificate: %w", err)
	}
	out := bytes.Buffer{}
	pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: cert})
	publicKey = out.String()
	out.Reset()
	pem.Encode(&out, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	privateKey = out.String()
	return privateKey, publicKey, nil
}

var droneIDAlphabet = []string{
	"alpha",
	"bravo",
	"charlie",
	"delta",
	"echo",
	"foxtrot",
	"golf",
	"hotel",
	"india",
	"juliet",
	"kilo",
	"lima",
	"mike",
	"november",
	"oscar",
	"papa",
	"quebec",
	"romeo",
	"sierra",
	"tango",
	"uniform",
	"victor",
	"whiskey",
	"xray",
	"yankee",
	"zulu",
}

func generateDroneID(c context.Context, simulationName string) (string, error) {
	drones, err := getDrones(c, simulationName)
	if err != nil {
		return "", fmt.Errorf("could not get existing drone ids: %w", err)
	}
	availableLetters := make([]string, 0)
	for _, l := range droneIDAlphabet {
		available := true
		for _, d := range drones {
			if strings.HasPrefix(d.DeviceID, l) {
				available = false
				break
			}
		}
		if !available {
			continue
		}
		availableLetters = append(availableLetters, l)
	}
	if len(availableLetters) != 0 {
		// still free letters
		return availableLetters[rand.Intn(len(availableLetters))], nil
	}

	// no free letters
	for i := 0; i < 20; i++ {
		letter := droneIDAlphabet[rand.Intn(len(droneIDAlphabet))]

		id := fmt.Sprintf("%s%s", letter, strings.Split(uuid.New().String(), "-")[3])
		used := false
		for _, d := range drones {
			if d.DeviceID == id {
				used = true
				break
			}
		}
		if used {
			continue
		}
		return id, nil
	}

	return "", errors.New("could not generate unique drone id in 20 tries")
}

func isDroneIDAvailable(c context.Context, clientset *kubernetes.Clientset, simulationName string, droneID string) bool {
	drones, err := getDrones(c, simulationName)
	if err != nil {
		return false
	}
	for _, d := range drones {
		if d.DeviceID == droneID {
			return false
		}
	}
	return true
}

type drone struct {
	DeviceID      string `json:"device_id"`
	DroneLocation string `json:"drone_location"`
}

func getDrones(ctx context.Context, simulationName string) ([]drone, error) {
	var resp []drone
	url := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/drones", simulationName)
	if err := getJSON(ctx, url, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func waitLoadBalancerIP(c context.Context, namespace string, serviceName string) (string, error) {
	kube := getKube()
	for {
		lb, err := kube.CoreV1().Services(namespace).Get(c, serviceName, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		if len(lb.Status.LoadBalancer.Ingress) > 0 {
			return lb.Status.LoadBalancer.Ingress[0].IP, nil
		}

		select {
		case <-c.Done():
			// timeout/cancel
			return "", nil
		case <-time.After(500 * time.Millisecond):
			// continue polling
		}
	}
}

func waitDeploymentAvailable(ctx context.Context, namespace, name string) error {
	clientset := getKube()
	for {
		dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			if dep.Status.AvailableReplicas > 0 {
				return nil
			}
		} else if !k8serrors.IsNotFound(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func getShortestUniqueSimulationName(c context.Context, kube *kubernetes.Clientset, names []string) string {

	namespaces, err := kube.CoreV1().Namespaces().List(c, metav1.ListOptions{LabelSelector: "dronsole-type=simulation"})
	if err != nil {
		panic(err)
	}
	// start from shortest possible and end when first unique is found with that length
	for i := 5; i < 22; i++ {
		for _, name := range names {
			nameCandidate := name[:i]
			found := true
			for _, ns := range namespaces.Items {
				if strings.HasPrefix(ns.Name, nameCandidate) {
					found = false
					break
				}
			}
			if found {
				return nameCandidate
			}
		}
	}
	panic("Could not find unique name")
}

func generateSimulationName(c context.Context, kube *kubernetes.Clientset) string {
	names := make([]string, 20)
	for i := 0; i < 20; i++ {
		names[i] = fmt.Sprintf("sim-%s", uuid.New().String())
	}

	return getShortestUniqueSimulationName(c, kube, names)
}

func getDronesHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")

	resp, err := getDrones(c, simulationName)
	if err != nil {
		writeServerError(w, "could not get list of drones", err)
		return
	}
	writeJSON(w, obj{"drones": resp})
}

func commandDroneHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "invalid request body", err)
		return
	}
	switch req.Command {
	case "takeoff", "land":
	default:
		writeBadRequest(w, "unsupported command", nil)
		return
	}

	simType, err := getSimulationType(c, simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	mqttServer := mqttServerURL
	if simType == kube.SimulationStandalone {
		mqttServer = fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)
	}
	client, err := getIotCoreClient(c, mqttServer)
	if err != nil {
		writeServerError(w, "failed to connect to command server", err)
		return
	}
	defer client.Close()
	err = client.SendCommand(c, droneID, "control", obj{
		"Command":   req.Command,
		"Timestamp": time.Now(),
	})
	if err != nil {
		writeServerError(w, "could not publish command", err)
	}
}

func droneLogStreamHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")
	clientset := getKube()

	pods, err := clientset.CoreV1().Pods(simulationName).List(c, metav1.ListOptions{LabelSelector: "drone-device-id=" + droneID})
	if err != nil {
		writeServerError(w, "failed to get drone", err)
		return
	}
	if len(pods.Items) != 1 {
		writeNotFound(w, "the requested drone does not exist", nil)
		return
	}
	req := clientset.CoreV1().Pods(simulationName).GetLogs(pods.Items[0].Name, &v1.PodLogOptions{
		Follow: false,
	})
	result, err := req.DoRaw(c)
	if err != nil {
		writeServerError(w, "failed to retreive logs", err)
		return
	}
	w.Write(result)
}

func droneEventStreamHandler(w http.ResponseWriter, r *http.Request) {
	c, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	conn, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		writeBadRequest(w, "failed to upgrade connection to websocket", err)
		return
	}
	defer conn.Close()
	var req struct {
		Path string `json:"path"`
	}
	if err = conn.ReadJSON(&req); err != nil {
		writeWSError(conn, "failed to read input message", err)
		return
	}
	if req.Path == "" || req.Path[0] != '/' {
		req.Path = "/" + req.Path
	}
	if req.Path[len(req.Path)-1] != '/' {
		req.Path = req.Path + "/"
	}

	simType, err := getSimulationType(c, simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	mqttServer := mqttServerURL
	if simType == kube.SimulationStandalone {
		mqttServer = fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)
	}
	client, err := getIotCoreClient(c, mqttServer)
	if err != nil {
		writeServerError(w, "failed to connect to command server", err)
		return
	}
	defer client.Close()
	err = client.Subscribe(c, droneID, req.Path, func(msg *subscriptionMessage) {
		if err := conn.WriteJSON(msg); err != nil {
			log.Println("failed to send event:", err)
			cancel()
		}
	})
	if err != nil {
		writeWSError(conn, "Could not subscribe", err)
		return
	}
}

func droneVideoStreamHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	simType, err := getSimulationType(r.Context(), simulationName)
	if err != nil {
		writeServerError(w, "failed to get simulation type", err)
		return
	}
	mqttServer := mqttServerURL
	videoServer := videoServerHost
	if simType == kube.SimulationStandalone {
		mqttServer = fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)
		videoServer, err = waitLoadBalancerIP(r.Context(), simulationName, "video-server-svc")
		if err != nil {
			writeServerError(w, "error getting video server address", err)
			return
		}
		videoServer += ":8554"
	}
	client, err := getIotCoreClient(r.Context(), mqttServer)
	if err != nil {
		writeServerError(w, "failed to connect to command server", err)
		return
	}
	defer client.Close()
	err = client.SendCommand(r.Context(), droneID, "videostream", obj{
		"Command": "start",
		"Address": fmt.Sprintf("rtsp://%s:%s@%s/%s", videoServerUsername, videoServerPassword, videoServer, droneID),
	})
	if err != nil {
		writeServerError(w, "could not publish command", err)
		return
	}
	writeJSON(w, obj{
		"video_url": fmt.Sprintf("rtsp://%s/%s", videoServer, droneID),
	})
}

func droneShellHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	params := httprouter.ParamsFromContext(ctx)
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	podName, err := kube.GetDronePodName(ctx, simulationName, droneID, getKube())
	if errors.Is(err, kube.ErrNoSuchDrone) {
		writeNotFound(w, "the drone does not exist", nil)
		return
	} else if err != nil {
		writeServerError(w, "could not find pod from cluster", err)
		return
	}
	clientConfig, err := rest.InClusterConfig()
	if err != nil {
		writeServerError(w, "failed to get cluster config", err)
		return
	}
	req := getKube().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(simulationName).
		SubResource("exec")
	req.VersionedParams(&v1.PodExecOptions{
		Command: []string{"bash"},
		Stdin:   true,
		Stdout:  true,
		Stderr:  true,
		TTY:     true,
	}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(clientConfig, "POST", req.URL())
	if err != nil {
		writeServerError(w, "failed to connect drone", err)
		return
	}
	conn, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		writeBadRequest(w, "failed to upgrade connection to websocket", err)
		return
	}
	defer func() {
		log.Println("connection closed:", conn.Close())
	}()
	sizeQueue := newTerminalSizeQueue()
	defer sizeQueue.Close()
	inReader, inWriter := io.Pipe()
	defer inReader.Close()
	defer inWriter.Close()
	outWriter := &ttyWriter{conn: conn}
	go func() (err error) {
		defer func() {
			cancel()
			if err != nil {
				log.Println(err)
			}
		}()
		for ctx.Err() == nil {
			_, reader, err := conn.NextReader()
			if err != nil {
				return err
			}
			r := bufio.NewReader(reader)
			msgType, err := r.ReadByte()
			if err != nil {
				return err
			}
			switch msgType {
			case 'd': // message contains data from client stdin
				if _, err = io.Copy(inWriter, r); err != nil {
					return err
				}
			case 's': // message contains terminal size change event
				var sizeChange remotecommand.TerminalSize
				if err = json.NewDecoder(r).Decode(&sizeChange); err != nil {
					return err
				}
				sizeQueue.Push(&sizeChange)
			default: // unknown message type
				return fmt.Errorf("invalid input message type: %c (%[1]d)", msgType)
			}
		}
		return nil
	}()
	go func() {
		defer cancel()
		err = exec.Stream(remotecommand.StreamOptions{
			Stdin:             inReader,
			Stdout:            outWriter,
			Stderr:            outWriter,
			TerminalSizeQueue: sizeQueue,
			Tty:               true,
		})
		cancel()
		if err != nil {
			log.Printf("exec stream returned an error: %v", err)
		}
	}()
	<-ctx.Done()
}

type ttyWriter struct {
	prefix []byte
	conn   *websocket.Conn
	mutex  sync.Mutex
}

func (w *ttyWriter) Write(p []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	wr, err := w.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	defer wr.Close()
	n, err := wr.Write(w.prefix)
	if err != nil {
		return n, err
	}
	n2, err := wr.Write(p)
	return n + n2, err
}

type terminalSizeQueue struct {
	closed bool
	mutex  sync.Mutex
	ch     chan *remotecommand.TerminalSize
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{ch: make(chan *remotecommand.TerminalSize)}
}

func (q *terminalSizeQueue) Push(size *remotecommand.TerminalSize) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if !q.closed {
		q.ch <- size
	}
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	return <-q.ch
}

func (q *terminalSizeQueue) Close() {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if !q.closed {
		close(q.ch)
		q.closed = true
	}
}
