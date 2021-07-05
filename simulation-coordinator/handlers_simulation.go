package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/tiiuae/fleet-management/simulation-coordinator/kube"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	imageGZServer         = "ghcr.io/tiiuae/tii-gzserver"
	imageGZWeb            = "ghcr.io/tiiuae/tii-gzweb"
	imageFogDrone         = "ghcr.io/tiiuae/tii-fog-drone:f4f-int"
	imageMQTTServer       = "ghcr.io/tiiuae/tii-mqtt-server:latest"
	imageMissionControl   = "ghcr.io/tiiuae/tii-mission-control:latest"
	imageVideoServer      = "ghcr.io/tiiuae/tii-video-server:latest"
	imageVideoMultiplexer = "ghcr.io/tiiuae/tii-video-multiplexer:latest"
	imageWebBackend       = "ghcr.io/tiiuae/tii-web-backend:latest"
)

var (
	imageGPUTag = map[SimulationGPUMode]string{
		SimulationGPUModeNone:   ":latest",
		SimulationGPUModeNvidia: ":nvidia",
	}
)

var websocketUpgrader websocket.Upgrader

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

	mqttIP, err := waitLoadBalancerIP(r.Context(), simulationName, "mqtt-server-svc")
	if err != nil {
		writeServerError(w, "Error getting mqtt server address", err)
		return
	}
	writeJSON(w, obj{"mqtt_server": obj{"url": "tcp://" + mqttIP + ":8883"}})
}

func kubeSimNamespace(namespace string, standalone bool) *v1.Namespace {
	simType := "global"
	if standalone {
		simType = "standalone"
	}
	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"dronsole-type":            "simulation",
				"dronsole-simulation-type": simType,
			},
			Annotations: map[string]string{
				"dronsole-expiration-timestamp": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
			},
		},
	}
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
							ImagePullPolicy: v1.PullIfNotPresent,
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
							ImagePullPolicy: v1.PullIfNotPresent,
							Env:             env,
							VolumeMounts:    volumeMounts,
						},
					},
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

	ns := kubeSimNamespace(request.Name, request.Standalone)
	ns, err = clientset.CoreV1().Namespaces().Create(c, ns, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create namespace for the simulation", err, http.StatusInternalServerError)
		return
	}

	gzserverDeployment := kubeSimGZServerDeployment(request.DataImage)
	gzserverDeployment, err = clientset.AppsV1().Deployments(request.Name).Create(c, gzserverDeployment, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create gzserver deployment", err, http.StatusInternalServerError)
		err = clientset.CoreV1().Namespaces().Delete(c, request.Name, metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after gzserver deployment creation failed: %v", err))
		}
		return
	}

	gzserverService := kubeSimGZServerService()
	gzserverService, err = clientset.CoreV1().Services(request.Name).Create(c, gzserverService, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create gzserver service", err, http.StatusInternalServerError)
		err = clientset.CoreV1().Namespaces().Delete(c, request.Name, metav1.NewDeleteOptions(10))
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
		err = clientset.CoreV1().Namespaces().Delete(c, request.Name, metav1.NewDeleteOptions(10))
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

	err := kube.CoreV1().Namespaces().Delete(c, simulationName, metav1.NewDeleteOptions(10))
	if err != nil {
		writeError(w, "Could not delete simulation", err, http.StatusInternalServerError)
		return
	}
}

func createViewer(c context.Context, namespace string, image string) error {
	kube := getKube()
	_, err := kube.AppsV1().Deployments(namespace).Get(c, "gzweb-dep", metav1.GetOptions{})
	if err == nil {
		// the deployment already exists
		return nil
	}

	// get gzserver deployment
	gzserverDep, err := kube.AppsV1().Deployments(namespace).Get(c, "gzserver-dep", metav1.GetOptions{})
	if err != nil {
		log.Printf("Unable to get gzserver-dep")
		return err
	}

	dataImage := gzserverDep.Spec.Template.Spec.InitContainers[0].Image

	gzwebDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gzweb-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "gzweb-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "gzweb-pod",
					},
				},
				Spec: v1.PodSpec{
					Volumes: []v1.Volume{
						{
							Name: "gazebo-data-vol",
							VolumeSource: v1.VolumeSource{
								EmptyDir: &v1.EmptyDirVolumeSource{},
							},
						},
					},
					InitContainers: []v1.Container{
						{
							Name:            "gazebo-data",
							Image:           dataImage,
							ImagePullPolicy: v1.PullIfNotPresent,
							Command:         []string{"cp", "-r", "/gazebo-data/models", "/gazebo-data/worlds", "/gazebo-data/scripts", "gazebo-data/plugins", "/data"},
							VolumeMounts: []v1.VolumeMount{
								{
									MountPath: "/data",
									Name:      "gazebo-data-vol",
								},
							},
						},
					},
					Containers: []v1.Container{
						{
							Name:            "gzweb",
							Image:           image,
							Args:            []string{"http://gzserver-svc:11345"},
							ImagePullPolicy: v1.PullIfNotPresent,
							VolumeMounts: []v1.VolumeMount{
								{
									MountPath: "/data",
									Name:      "gazebo-data-vol",
								},
							},
						},
					},
				},
			},
		},
	}

	gzwebDeployment, err = kube.AppsV1().Deployments(namespace).Create(c, gzwebDeployment, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating gzweb deployment %v", err)
		return err
	}

	gzwebService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gzweb-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt(8080),
			}},
			Selector: map[string]string{"app": "gzweb-pod"},
			Type:     "LoadBalancer",
		},
	}

	gzwebService, err = kube.CoreV1().Services(namespace).Create(c, gzwebService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating gzweb service %v", err)
		return err
	}
	return nil
}

func startViewerHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")

	err := createViewer(c, simulationName, imageGZWeb+":latest")
	if err != nil {
		writeServerError(w, "Unable to create viewer", err)
		return
	}
	waitCtx, cancelWait := context.WithTimeout(c, 10*time.Second)
	defer cancelWait()
	ip, err := waitLoadBalancerIP(waitCtx, simulationName, "gzweb-svc")
	if err != nil {
		writeServerError(w, "Unable to get IP for gzweb", err)
		return
	}
	if ip == "" {
		writeServerError(w, "Timeout while trying to get IP for gzweb", nil)
		return
	}
	writeJSON(w, obj{"url": fmt.Sprintf("http://%s/", ip)})
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
		kube := getKube()

		name := fmt.Sprintf("drone-%s", request.DroneID)

		droneContainerEnvs := []v1.EnvVar{
			{
				Name:  "DRONE_DEVICE_ID",
				Value: request.DroneID,
			},
			{
				Name:  "DRONE_IDENTITY_KEY",
				Value: request.PrivateKey,
			},
			{
				Name:  "MQTT_BROKER_ADDRESS",
				Value: "tcp://mqtt-server-svc:8883",
			},
			{
				Name:  "RTSP_SERVER_ADDRESS",
				Value: "DroneUser:22f6c4de-6144-4f6c-82ea-8afcdf19f316@video-server-svc:8554",
			},
			{
				Name:  "MISSION_DATA_RECORDER_BACKEND_URL",
				Value: "http://mission-data-recorder-backend-svc:9423",
			},
		}

		droneDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": name,
					},
				},
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app":             name,
							"drone-device-id": request.DroneID,
						},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:            name,
								Image:           imageFogDrone,
								ImagePullPolicy: v1.PullIfNotPresent,
								Env:             droneContainerEnvs,
							},
						},
					},
				},
			},
		}

		droneDeployment, err = kube.AppsV1().Deployments(simulationName).Create(c, droneDeployment, metav1.CreateOptions{})
		if err != nil {
			writeError(w, "Could not create drone deployment", err, http.StatusInternalServerError)
			return
		}

		droneService := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-svc", name),
			},
			Spec: v1.ServiceSpec{
				Type: v1.ServiceTypeClusterIP,
				Ports: []v1.ServicePort{
					{
						Name:     "mavlink-udp",
						Port:     14560,
						Protocol: "UDP",
					},
					{
						Name:     "gst-cam-udp",
						Port:     5600,
						Protocol: "UDP",
					},
				},
				Selector: map[string]string{
					"app": name,
				},
			},
		}
		droneService, err = kube.CoreV1().Services(simulationName).Create(c, droneService, metav1.CreateOptions{})
		if err != nil {
			writeError(w, "Could not create drone service", err, http.StatusInternalServerError)
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

	var resp []struct {
		DeviceID      string `json:"device_id"`
		DroneLocation string `json:"drone_location"`
	}
	url := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/drones", simulationName)
	if err := getJSON(c, url, &resp); err != nil {
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

	// generate MQTT client
	// configure MQTT client
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)).
		SetClientID("dronsole").
		SetUsername("dronsole").
		//SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}).
		SetPassword("").
		SetProtocolVersion(4) // Use MQTT 3.1.1

	client := mqtt.NewClient(opts)

	tok := client.Connect()
	if !tok.WaitTimeout(time.Second * 5) {
		writeServerError(w, "MQTT connection timeout", nil)
		return
	}
	if err := tok.Error(); err != nil {
		writeServerError(w, "Could not connect to MQTT", err)
		return
	}
	defer client.Disconnect(1000)

	msg, err := json.Marshal(obj{
		"Command":   req.Command,
		"Timestamp": time.Now(),
	})
	if err != nil {
		writeServerError(w, "Could not marshal command", err)
		return
	}

	pubtok := client.Publish(fmt.Sprintf("/devices/%s/commands/control", droneID), 1, false, msg)
	if !pubtok.WaitTimeout(time.Second * 2) {
		writeServerError(w, "Publish timeout", nil)
		return
	}
	if err = pubtok.Error(); err != nil {
		writeServerError(w, "Could not publish command", err)
		return
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
	c := r.Context()
	params := httprouter.ParamsFromContext(r.Context())
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

	// generate MQTT client
	// configure MQTT client
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)).
		SetClientID(fmt.Sprintf("dronsole-events-%s", droneID)).
		SetUsername("dronsole").
		//SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}).
		SetPassword("").
		SetProtocolVersion(4) // Use MQTT 3.1.1

	client := mqtt.NewClient(opts)

	tok := client.Connect()
	if !tok.WaitTimeout(time.Second * 5) {
		writeWSError(conn, "MQTT connection timeout", nil)
		return
	}
	if err := tok.Error(); err != nil {
		writeWSError(conn, "Could not connect to MQTT", err)
		return
	}
	defer client.Disconnect(1000)

	c, cancel := context.WithCancel(c)
	prefix := fmt.Sprintf("/devices/%s/events", droneID)
	token := client.Subscribe(fmt.Sprintf("/devices/%s/events%s#", droneID, req.Path), 0, func(client mqtt.Client, msg mqtt.Message) {
		err = conn.WriteJSON(obj{
			"topic":   strings.TrimPrefix(msg.Topic(), prefix),
			"message": string(msg.Payload()),
		})
		if err != nil {
			log.Println("failed to send event:", err)
			cancel()
		}
	})
	if err := token.Error(); err != nil {
		writeWSError(conn, "Could not subscribe", err)
		return
	}
	<-c.Done()
}

func droneVideoStreamHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	droneID := params.ByName("droneID")

	videoServerIP, err := waitLoadBalancerIP(r.Context(), simulationName, "video-server-svc")
	if err != nil {
		writeServerError(w, "error getting video server address", err)
		return
	}

	// generate MQTT client
	// configure MQTT client
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("mqtt-server-svc.%s:8883", simulationName)).
		SetClientID("dronsole").
		SetUsername("dronsole").
		//SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}).
		SetPassword("").
		SetProtocolVersion(4) // Use MQTT 3.1.1

	client := mqtt.NewClient(opts)

	tok := client.Connect()
	if !tok.WaitTimeout(time.Second * 5) {
		writeServerError(w, "MQTT connection timeout", err)
		return
	}
	if err := tok.Error(); err != nil {
		writeServerError(w, "Could not connect to MQTT", err)
		return
	}
	defer client.Disconnect(1000)

	msg, err := json.Marshal(obj{
		"Command": "start",
		"Address": fmt.Sprintf("rtsp://%s:%s@%s:8554/%s", "DroneUser", "22f6c4de-6144-4f6c-82ea-8afcdf19f316", "video-server-svc", droneID),
	})
	if err != nil {
		writeServerError(w, "Could not marshal start video command", err)
		return
	}

	pubtok := client.Publish(fmt.Sprintf("/devices/%s/commands/videostream", droneID), 1, false, msg)
	if !pubtok.WaitTimeout(time.Second * 2) {
		writeServerError(w, "Publish timeout", err)
		return
	}
	if err = pubtok.Error(); err != nil {
		writeServerError(w, "Could not publish takeoff", err)
		return
	}
	writeJSON(w, obj{
		"video_url": fmt.Sprintf("rtsp://%s:8554/%s", videoServerIP, droneID),
	})
}
