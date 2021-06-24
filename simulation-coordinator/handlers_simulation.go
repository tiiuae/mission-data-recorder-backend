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

	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	imageGZServer = "ghcr.io/tiiuae/tii-gzserver"
	imageGZWeb    = "ghcr.io/tiiuae/tii-gzweb"
	imageFogDrone = "ghcr.io/tiiuae/tii-fog-drone:f4f-int"
)

var (
	imageGPUTag = map[SimulationGPUMode]string{
		SimulationGPUModeNone:   ":latest",
		SimulationGPUModeNvidia: ":nvidia",
	}
)

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
	writeJSON(w, response)
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

	kube := getKube()
	name := generateSimulationName(c, kube)
	log.Printf("Creating simulation %s with world %s", name, request.World)

	ns := kubeSimNamespace(name, request.Standalone)
	ns, err = kube.CoreV1().Namespaces().Create(c, ns, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create namespace for the simulation", err, http.StatusInternalServerError)
		return
	}

	gzserverDeployment := kubeSimGZServerDeployment(request.DataImage)
	gzserverDeployment, err = kube.AppsV1().Deployments(name).Create(c, gzserverDeployment, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create gzserver deployment", err, http.StatusInternalServerError)
		err = kube.CoreV1().Namespaces().Delete(c, name, metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after gzserver deployment creation failed: %v", err))
		}
		return
	}

	gzserverService := kubeSimGZServerService()
	gzserverService, err = kube.CoreV1().Services(name).Create(c, gzserverService, metav1.CreateOptions{})
	if err != nil {
		writeError(w, "Could not create gzserver service", err, http.StatusInternalServerError)
		err = kube.CoreV1().Namespaces().Delete(c, name, metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after gzserver service creation failed: %v", err))
		}
		return
	}

	// request world creation
	requestBody, err := json.Marshal(struct {
		WorldFile string `json:"world_file"`
	}{
		WorldFile: request.World,
	})
	if err != nil {
		panic("Could not marshal body")
	}

	// start the simulation by calling the service
	startURL := fmt.Sprintf("http://gzserver-svc.%s:8081/simulation/start", name)
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
		err = kube.CoreV1().Namespaces().Delete(c, name, metav1.NewDeleteOptions(10))
		if err != nil {
			panic(fmt.Sprintf("Unable to delete namespace after simulation start on gzserver failed: %v", err))
		}
		return
	}

	log.Printf("Simulation started")
	writeJSON(w, name)
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
							ImagePullPolicy: v1.PullAlways,
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
							ImagePullPolicy: v1.PullAlways,
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
		writeError(w, "Unable to create viewer", err, http.StatusInternalServerError)
		return
	}
	waitCtx, cancelWait := context.WithTimeout(c, 10*time.Second)
	defer cancelWait()
	ip, err := waitLoadBalancerIP(waitCtx, simulationName, "gzweb-svc")
	if err != nil {
		writeError(w, "Unable to get IP for gzweb", err, http.StatusInternalServerError)
		return
	}
	if ip == "" {
		writeError(w, "Timeout while trying to get IP for gzweb", nil, http.StatusInternalServerError)
		return
	}
	writeJSON(w, fmt.Sprintf("http://%s/", ip))
}

func addDroneHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	params := httprouter.ParamsFromContext(c)
	simulationName := params.ByName("simulationName")
	var request struct {
		DroneID    string  `json:"drone_id"`
		PrivateKey string  `json:"private_key"`
		PosX       float64 `json:"pos_x"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Could not unmarshal simulation request", err, http.StatusInternalServerError)
		return
	}
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
			Value: "ssl://mqtt.googleapis.com:8883",
		},
		{
			Name:  "RTSP_SERVER_ADDRESS",
			Value: "DroneUser:22f6c4de-6144-4f6c-82ea-8afcdf19f316@video-stream.sacplatform.com:8555",
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
							ImagePullPolicy: v1.PullAlways,
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

	requestBody, err := json.Marshal(struct {
		DroneLocation  string  `json:"drone_location"`
		DeviceID       string  `json:"device_id"`
		MAVLinkAddress string  `json:"mavlink_address"`
		MAVLinkUDPPort int32   `json:"mavlink_udp_port"`
		VideoUDPPort   int32   `json:"video_udp_port"`
		PosX           float64 `json:"pos_x"`
		PosY           float64 `json:"pos_y"`
		PosZ           float64 `json:"pos_z"`
		Yaw            float64 `json:"yaw"`
		Pitch          float64 `json:"pitch"`
		Roll           float64 `json:"roll"`
	}{
		DroneLocation:  "cluster",
		DeviceID:       request.DroneID,
		MAVLinkAddress: fmt.Sprintf("drone-%s-svc", request.DroneID),
		MAVLinkUDPPort: 14560,
		VideoUDPPort:   5600,
		PosX:           request.PosX,
		PosY:           0,
		PosZ:           0,
		Yaw:            0,
		Pitch:          0,
		Roll:           0,
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
