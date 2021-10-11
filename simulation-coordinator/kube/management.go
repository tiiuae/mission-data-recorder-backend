package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/oauth2/google"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const nonExpiringDuration = 100 * 365 * 24 * time.Hour

func currentExpiryTime(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339)
}

type SimulationType string

const (
	SimulationGlobal     SimulationType = "global"
	SimulationStandalone SimulationType = "standalone"
)

type SimulationGPUMode int

const (
	SimulationGPUModeNone SimulationGPUMode = iota
	SimulationGPUModeNvidia
)

func (m SimulationGPUMode) String() string {
	switch m {
	case SimulationGPUModeNone:
		return "none"
	case SimulationGPUModeNvidia:
		return "nvidia"
	default:
		return ""
	}
}

func (m *SimulationGPUMode) Set(s string) error {
	switch s {
	case "none":
		*m = SimulationGPUModeNone
	case "nvidia":
		*m = SimulationGPUModeNvidia
	default:
		return fmt.Errorf("unknown simulation GPU mode: %s", s)
	}
	return nil
}

var (
	ErrNoSuchDrone           = errors.New("kube: no such drone")
	ErrDroneExists           = errors.New("drone already exists")
	ErrSimulationDoesntExist = errors.New("simulation doesn't exist")
)

const (
	globalPortRangeStart  = 38400
	portRangeSize         = 5
	videoServerPortOffset = 0
	gzwebPortOffset       = 1
	mqttServerPortOffset  = 2
	gzserverPortOffset    = 3
)

type Client struct {
	PullPolicy v1.PullPolicy

	Clientset *kubernetes.Clientset
}

func NewInClusterConfig() (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	c := &Client{PullPolicy: v1.PullIfNotPresent}
	c.Clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func NewOutClusterConfig() (*Client, error) {
	homedir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configpath := filepath.Join(homedir, ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags("", configpath)
	if err != nil {
		return nil, err
	}

	c := &Client{PullPolicy: v1.PullNever}

	c.Clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return c, err
}

func (c *Client) CopySecret(ctx context.Context, fromNamespace, fromName, toNamespace, toName string) error {
	secret, err := c.Clientset.CoreV1().Secrets(fromNamespace).Get(ctx, fromName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	secret = &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      toName,
			Namespace: toNamespace,
		},
		Type: secret.Type,
		Data: secret.Data,
	}
	_, err = c.Clientset.CoreV1().Secrets(toNamespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func (c *Client) getPortRange(ctx context.Context, namespace string) (int32, error) {
	ns, err := c.Clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	p, err := strconv.ParseInt(ns.ObjectMeta.Annotations["dronsole-port-range-start"], 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(p), nil
}

func (c *Client) getFirstFreePortRange(ctx context.Context) (int, error) {
	nss, err := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	var errs *multierror.Error
	var ports sort.IntSlice
	for _, ns := range nss.Items {
		portStr, ok := ns.ObjectMeta.Annotations["dronsole-port-range-start"]
		if ok {
			port, err := strconv.Atoi(portStr)
			errs = multierror.Append(errs, err)
			ports = append(ports, port)
		}
	}
	sort.Sort(ports)
	current := globalPortRangeStart
	for _, port := range ports {
		if port < current {
			continue
		} else if port >= current+portRangeSize {
			return current, errs.ErrorOrNil()
		}
		current += portRangeSize
	}
	return current, errs.ErrorOrNil()
}

type Simulation struct {
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Owners    StringSet `json:"-"`
}

type CreateNamespaceOptions struct {
	Name, ID       string
	SimType        SimulationType
	ExpiryDuration time.Duration
	Owners         []string
}

func (c *Client) CreateNamespace(ctx context.Context, opts *CreateNamespaceOptions) (*v1.Namespace, error) {
	port, err := c.getFirstFreePortRange(ctx)
	if port == 0 {
		return nil, fmt.Errorf("failed to get a free port range: %w", err)
	}
	if err != nil {
		log.Println("errors occurred when searching for a free port range:", err)
	}
	owners, err := json.Marshal(opts.Owners)
	if err != nil {
		return nil, err
	}
	var expiryTime string
	if opts.ExpiryDuration == 0 {
		expiryTime = currentExpiryTime(nonExpiringDuration)
	} else {
		expiryTime = currentExpiryTime(opts.ExpiryDuration)
	}
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.Name,
			Labels: map[string]string{
				"dronsole-type":            "simulation",
				"dronsole-simulation-type": string(opts.SimType),
			},
			Annotations: map[string]string{
				"dronsole-expiration-timestamp": expiryTime,
				"dronsole-expiry-duration":      opts.ExpiryDuration.String(),
				"dronsole-simulation-id":        opts.ID,
				"dronsole-port-range-start":     strconv.Itoa(port),
				"dronsole-owners":               string(owners),
			},
		},
	}
	return c.Clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
}

func (c *Client) GetSimulation(ctx context.Context, name string) (*v1.Namespace, error) {
	ns, err := c.Clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) || (err == nil && ns.Labels["dronsole-type"] != "simulation") {
		return nil, ErrSimulationDoesntExist
	} else if err != nil {
		return nil, err
	}
	return ns, nil
}

func (c *Client) GetSimulations(ctx context.Context) ([]Simulation, error) {
	ns, err := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "dronsole-type=simulation",
	})
	if err != nil {
		return nil, err
	}
	sims := make([]Simulation, len(ns.Items))
	for i, n := range ns.Items {
		sims[i] = Simulation{
			Name:      n.Name,
			Phase:     string(n.Status.Phase),
			Type:      n.Labels["dronsole-simulation-type"],
			CreatedAt: n.CreationTimestamp.Time,
			Owners:    StringSet{},
		}
		err = json.Unmarshal([]byte(n.Annotations["dronsole-owners"]), &sims[i].Owners)
		if err != nil {
			log.Printf("failed to parse owners for simulation %s: %v", n.Name, err)
		}
		sims[i].ExpiresAt, err = time.Parse(time.RFC3339, n.Annotations["dronsole-expiration-timestamp"])
		if err != nil {
			log.Printf("an error occurred when parsing expiration timestamp for simulation %s: %v", n.Name, err)
		}
	}
	return sims, nil
}

type StringSet map[string]bool

func (s *StringSet) UnmarshalJSON(data []byte) error {
	*s = StringSet{}
	if len(data) == 0 {
		return nil
	}
	var owners []string
	if err := json.Unmarshal(data, &owners); err != nil {
		return err
	}
	for _, owner := range owners {
		(*s)[owner] = true
	}
	return nil
}

func (c *Client) GetSimulationOwners(ctx context.Context, name string) (StringSet, error) {
	sim, err := c.GetSimulation(ctx, name)
	if err != nil {
		return nil, err
	}
	var owners StringSet
	err = json.Unmarshal([]byte(sim.Annotations["dronsole-owners"]), &owners)
	if err != nil {
		return nil, err
	}
	return owners, nil
}

func (c *Client) GetSimulationType(ctx context.Context, simulationName string) (SimulationType, error) {
	ns, err := c.GetSimulation(ctx, simulationName)
	if k8serrors.IsNotFound(err) {
		return "", err
	}
	simType := ns.Labels["dronsole-simulation-type"]
	switch SimulationType(simType) {
	case SimulationGlobal:
	case SimulationStandalone:
	case "":
		return "", ErrSimulationDoesntExist
	default:
		return "", errors.New("invalid simulation type: " + simType)
	}
	return SimulationType(simType), nil
}

func (c *Client) RemoveSimulation(ctx context.Context, name string) error {
	ns, err := c.Clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) || (err == nil && ns.Labels["dronsole-type"] != "simulation") {
		return ErrSimulationDoesntExist
	} else if err != nil {
		return err
	}
	return c.Clientset.CoreV1().Namespaces().Delete(ctx, name, *metav1.NewDeleteOptions(5))
}

func (c *Client) RefreshSimulationExpiryTime(ctx context.Context, name string) error {
	ns, err := c.Clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) || (err == nil && ns.Labels["dronsole-type"] != "simulation") {
		return ErrSimulationDoesntExist
	} else if err != nil {
		return err
	}
	expiryDuration, err := time.ParseDuration(ns.Annotations["dronsole-expiry-duration"])
	if err != nil {
		return err
	}
	if expiryDuration == 0 {
		return nil
	}
	ns.Annotations["dronsole-expiration-timestamp"] = currentExpiryTime(expiryDuration)
	_, err = c.Clientset.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
	return err
}

func (c *Client) RemoveExpiredSimulations(ctx context.Context) error {
	sims, err := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "dronsole-type = simulation",
	})
	if err != nil {
		return err
	}
	deleteOpts := *metav1.NewDeleteOptions(0)
	now := time.Now()
	var errs *multierror.Error
	for _, sim := range sims.Items {
		expiryTimeStr := sim.Annotations["dronsole-expiration-timestamp"]
		if expiryTimeStr != "" {
			expiryTime, err := time.Parse(time.RFC3339, expiryTimeStr)
			if err != nil {
				errs = multierror.Append(errs, err)
			} else if now.After(expiryTime) {
				errs = multierror.Append(errs,
					c.Clientset.CoreV1().Namespaces().Delete(ctx, sim.Name, deleteOpts),
				)
			}
		}
	}
	return errs.ErrorOrNil()
}

func (c *Client) CreateGZServer(ctx context.Context, namespace, gzserverImage, dataImage string, gpuMode SimulationGPUMode, cloudMode, outClusterMode bool) error {
	// Volume definitions
	volumeGazeboData := v1.Volume{
		Name: "gazebo-data-vol",
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
		},
	}
	hostPathDirOrCreate := v1.HostPathDirectoryOrCreate
	volumeXSOCK := v1.Volume{
		Name: "xsock",
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{
				Path: "/tmp/.X11-unix",
				Type: &hostPathDirOrCreate,
			},
		},
	}
	volumeXAUTH := v1.Volume{
		Name: "xauth",
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{
				Path: os.Getenv("XAUTHORITY"),
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

	volumes := []v1.Volume{volumeGazeboData}
	volumeMounts := []v1.VolumeMount{volumeMountGazeboData}
	env := []v1.EnvVar{}
	var affinity *v1.Affinity
	if gpuMode != SimulationGPUModeNone {
		// GPU acceleration needs X server resources from the host machine
		// will mount these to the gzserver
		volumes = append(volumes, volumeXSOCK, volumeXAUTH)
		volumeMounts = append(volumeMounts, volumeMountXSOCK, volumeMountXAUTH)
		env = append(env, v1.EnvVar{
			Name:  "DISPLAY",
			Value: os.Getenv("DISPLAY"),
		}, v1.EnvVar{
			Name:  "XAUTHORITY",
			Value: "/tmp/.docker.xauth",
		}, v1.EnvVar{
			Name:  "NO_XVFB",
			Value: "true",
		})
		if cloudMode {
			affinity = &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{{
							MatchExpressions: []v1.NodeSelectorRequirement{{
								Key:      "cloud.google.com/gke-accelerator",
								Operator: v1.NodeSelectorOpExists,
							}},
						}},
					},
				},
			}
		}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gzserver-dep",
			Namespace: namespace,
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
							Image:           gzserverImage,
							ImagePullPolicy: v1.PullIfNotPresent,
							Env:             env,
							VolumeMounts:    volumeMounts,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
					Affinity: affinity,
				},
			},
		},
	}
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gzserver-svc",
			Namespace: namespace,
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

	_, err := c.Clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	if outClusterMode {
		portRange, err := c.getPortRange(ctx, namespace)
		if err != nil {
			return err
		}
		publicService := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gzserver-public-svc",
				Namespace: namespace,
			},
			Spec: v1.ServiceSpec{
				Ports: []v1.ServicePort{{
					Port:       portRange + gzserverPortOffset,
					TargetPort: intstr.FromInt(8081),
				}},
				Selector: map[string]string{"app": "gzserver-pod"},
				Type:     v1.ServiceTypeLoadBalancer,
			},
		}
		_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, publicService, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}
	return err
}

func (c *Client) CreateMQTT(ctx context.Context, namespace string, image string, loadBalancer bool) error {

	mqttDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mqtt-server-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "mqtt-server-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "mqtt-server-pod",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "mqtt-server",
							Image:           image,
							ImagePullPolicy: c.PullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := c.Clientset.AppsV1().Deployments(namespace).Create(ctx, &mqttDeployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating mqtt-server deployment %w", err)
	}

	mqttService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mqtt-server-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port: 8883,
			}},
			Selector: map[string]string{"app": "mqtt-server-pod"},
		},
	}

	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, &mqttService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating mqtt-server service %w", err)
	}
	if loadBalancer {
		portRange, err := c.getPortRange(ctx, namespace)
		if err != nil {
			return err
		}
		publicService := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mqtt-server-public-svc",
			},
			Spec: v1.ServiceSpec{
				Ports: []v1.ServicePort{{
					Port:       portRange + mqttServerPortOffset,
					TargetPort: intstr.FromInt(8883),
				}},
				Selector: map[string]string{"app": "mqtt-server-pod"},
				Type:     v1.ServiceTypeLoadBalancer,
			},
		}
		_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, publicService, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("Error creating mqtt-server public service %w", err)
		}
	}
	return nil
}

func (c *Client) CreateMissionControl(ctx context.Context, namespace string, image string) error {

	missionControlDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mission-control-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "mission-control-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "mission-control-pod",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "mission-control",
							Image:           image,
							Args:            []string{"mission-control-svc:2222", "tcp://mqtt-server-svc:8883"},
							ImagePullPolicy: c.PullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := c.Clientset.AppsV1().Deployments(namespace).Create(ctx, &missionControlDeployment, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating misison-control deployment %v", err)
		return err
	}

	missionControlService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mission-control-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name: "api",
					Port: 8082,
				},
				{
					Name: "ssh",
					Port: 2222,
				},
			},
			Selector: map[string]string{"app": "mission-control-pod"},
			Type:     v1.ServiceTypeClusterIP,
		},
	}

	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, &missionControlService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating mission-control service %v", err)
		return err
	}

	return nil
}

func (c *Client) CreateVideoServer(ctx context.Context, namespace, image, cert, key string) error {
	portRange, err := c.getPortRange(ctx, namespace)
	if err != nil {
		return err
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-server-secret",
		},
		StringData: map[string]string{
			"server.crt": cert,
			"server.key": key,
		},
	}
	_, err = c.Clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	videoServerDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-server-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "video-server-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "video-server-pod",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "video-server",
							Image:           image,
							ImagePullPolicy: c.PullPolicy,
							VolumeMounts: []v1.VolumeMount{{
								Name:      "cert",
								ReadOnly:  true,
								MountPath: "/certs",
							}},
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
					Volumes: []v1.Volume{{
						Name: "cert",
						VolumeSource: v1.VolumeSource{
							Secret: &v1.SecretVolumeSource{
								SecretName: "video-server-secret",
							},
						},
					}},
				},
			},
		},
	}

	_, err = c.Clientset.AppsV1().Deployments(namespace).Create(ctx, &videoServerDeployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating video-server deployment %w", err)
	}

	videoServerService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-server-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port: 8554,
			}},
			Selector: map[string]string{"app": "video-server-pod"},
		},
	}

	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, &videoServerService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating video-server service %w", err)
	}

	publicService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-server-public-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port:       portRange + videoServerPortOffset,
				TargetPort: intstr.FromInt(8554),
			}},
			Selector: map[string]string{"app": "video-server-pod"},
			Type:     v1.ServiceTypeLoadBalancer,
		},
	}
	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, publicService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating video-server service %w", err)
	}
	return nil
}

func (c *Client) CreateVideoStreamer(ctx context.Context, namespace, image, baseURL string) error {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-streamer-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "video-streamer-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "video-streamer-pod",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Name:            "video-streamer",
						Image:           image,
						ImagePullPolicy: c.PullPolicy,
						Args: []string{
							"video-server-svc:8554",
							baseURL,
						},
					}},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-streamer-svc",
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"app": "video-streamer-pod",
			},
			Ports: []v1.ServicePort{{
				Name:       "api",
				Port:       80,
				TargetPort: intstr.FromInt(8084),
			}},
		},
	}
	_, err := c.Clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	return err
}

func (c *Client) CreateWebBackend(ctx context.Context, namespace string, image string) error {

	videoServerDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-backend-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "web-backend-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "web-backend-pod",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "web-backend",
							Image:           image,
							Args:            []string{"tcp://mqtt-server-svc:8883"},
							ImagePullPolicy: c.PullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := c.Clientset.AppsV1().Deployments(namespace).Create(ctx, &videoServerDeployment, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating web-backend deployment %v", err)
		return err
	}

	videoServerService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-backend-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port: 8083,
			}},
			Selector: map[string]string{"app": "web-backend-pod"},
			Type:     v1.ServiceTypeClusterIP,
		},
	}

	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, &videoServerService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating web-backend service %v", err)
		return err
	}

	return nil

}

type MissionDataRecorderBackendCloudOptions struct {
	RegistryID       string // required
	ProjectID        string // required
	Region           string // required
	Bucket           string // required
	JSONKey          string // required
	DataObjectPrefix string // optional
}

type MissionDataRecorderBackendOptions struct {
	Namespace string // required
	Image     string // required

	// One of the following must be non-empty
	DataDirectory string
	Cloud         *MissionDataRecorderBackendCloudOptions
}

func (c *Client) CreateMissionDataRecorderBackend(ctx context.Context, opts *MissionDataRecorderBackendOptions) error {
	if (opts.DataDirectory == "" && opts.Cloud == nil) || (opts.DataDirectory != "" && opts.Cloud != nil) {
		return errors.New("exactly one of opts.DataDirectory and opts.Cloud must be non-empty")
	}
	volumeMounts := []v1.VolumeMount{{
		Name:      "config",
		MountPath: "/app/config",
		ReadOnly:  true,
	}}
	volumes := []v1.Volume{{
		Name: "config",
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{
					Name: "mission-data-recorder-backend-config",
				},
			},
		},
	}}
	var config *v1.ConfigMap
	if opts.Cloud == nil {
		config = &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "mission-data-recorder-backend-config"},
			Data: map[string]string{
				"config.yaml": `
port: 9423
host: http://mission-data-recorder-backend-svc
fileStorageDirectory: /app/mission-data`,
			},
		}
		volumeMounts = append(volumeMounts, v1.VolumeMount{
			Name:      "mission-data",
			MountPath: "/app/mission-data",
			ReadOnly:  false,
		})
		hostPathType := v1.HostPathDirectoryOrCreate
		volumes = append(volumes, v1.Volume{
			Name: "mission-data",
			VolumeSource: v1.VolumeSource{
				HostPath: &v1.HostPathVolumeSource{
					Path: opts.DataDirectory,
					Type: &hostPathType,
				},
			},
		})
	} else {
		key, err := google.JWTConfigFromJSON([]byte(opts.Cloud.JSONKey))
		if err != nil {
			return err
		}
		volumeMounts = append(volumeMounts, v1.VolumeMount{
			Name:      "secret",
			ReadOnly:  true,
			MountPath: "/app/secrets",
		})
		volumes = append(volumes, v1.Volume{
			Name: "secret",
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: "mission-data-recorder-backend-secret",
				},
			},
		})
		config = &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mission-data-recorder-backend-config",
				Namespace: opts.Namespace,
			},
			Data: map[string]string{
				"config.yaml": fmt.Sprintf(`
port: 9423
host: http://mission-data-recorder-backend-svc
privateKeyFile: /app/secrets/key.json
account: %s
urlValidDuration: 10m
bucket: %s
dataObjectPrefix: %s
disableValidation: true
gcp:
  registryId: %s
  projectId: %s
  region: %s`,
					key.Email, opts.Cloud.Bucket, opts.Cloud.DataObjectPrefix,
					opts.Cloud.RegistryID, opts.Cloud.ProjectID, opts.Cloud.Region,
				),
			},
		}
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mission-data-recorder-backend-secret",
				Namespace: opts.Namespace,
			},
			StringData: map[string]string{
				"key.json": opts.Cloud.JSONKey,
			},
		}
		_, err = c.Clientset.CoreV1().Secrets(opts.Namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mission-data-recorder-backend-dep",
			Namespace: opts.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "mission-data-recorder-backend-pod"},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "mission-data-recorder-backend-pod"},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "mission-data-recorder-backend",
							Image:           opts.Image,
							ImagePullPolicy: c.PullPolicy,
							VolumeMounts:    volumeMounts,
						},
					},
					Volumes: volumes,
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mission-data-recorder-backend-svc",
			Namespace: opts.Namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": "mission-data-recorder-backend-pod"},
			Ports: []v1.ServicePort{{
				Name:       "default",
				Port:       80,
				TargetPort: intstr.FromInt(9423),
			}},
		},
	}
	_, err := c.Clientset.CoreV1().ConfigMaps(opts.Namespace).Create(ctx, config, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = c.Clientset.AppsV1().Deployments(opts.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = c.Clientset.CoreV1().Services(opts.Namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

type MissionDataRecordingOptions struct {
	BackendURL    string   // required
	SizeThreshold int      // required
	Topics        []string // required
}

type CreateDroneOptions struct {
	DeviceID             string                      // required
	PrivateKey           string                      // required
	Image                string                      // required
	Namespace            string                      // required
	MQTTBrokerAddress    string                      // required
	RTSPServerAddress    string                      // required
	MissionDataRecording MissionDataRecordingOptions // required
	//
	CommlinkYaml string
	RecorderYaml string
	FogBash      string
}

func (c *Client) CreateDrone(ctx context.Context, opts *CreateDroneOptions) error {
	false := false
	name := fmt.Sprintf("drone-%s", opts.DeviceID)
	droneSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-secret",
			Namespace: opts.Namespace,
		},
		StringData: map[string]string{
			"DRONE_IDENTITY_KEY": opts.PrivateKey,
		},
	}
	droneContainerEnvs := []v1.EnvVar{
		{
			Name:  "DRONE_DEVICE_ID",
			Value: opts.DeviceID,
		},
		{
			Name: "DRONE_IDENTITY_KEY",
			ValueFrom: &v1.EnvVarSource{
				SecretKeyRef: &v1.SecretKeySelector{
					LocalObjectReference: v1.LocalObjectReference{
						Name: droneSecret.ObjectMeta.Name,
					},
					Key:      "DRONE_IDENTITY_KEY",
					Optional: &false,
				},
			},
		},
		{
			Name:  "MQTT_BROKER_ADDRESS",
			Value: opts.MQTTBrokerAddress,
		},
		{
			Name:  "RTSP_SERVER_ADDRESS",
			Value: opts.RTSPServerAddress,
		},
		{
			Name:  "MISSION_DATA_RECORDER_BACKEND_URL",
			Value: opts.MissionDataRecording.BackendURL,
		},
		{
			Name:  "MISSION_DATA_RECORDER_SIZE_THRESHOLD",
			Value: strconv.Itoa(opts.MissionDataRecording.SizeThreshold),
		},
		{
			Name:  "MISSION_DATA_RECORDER_TOPICS",
			Value: strings.Join(opts.MissionDataRecording.Topics, ","),
		},
		{
			Name:  "DRONSOLE_COMMLINK_CONFIG",
			Value: opts.CommlinkYaml,
		},
		{
			Name:  "DRONSOLE_RECORDER_CONFIG",
			Value: opts.RecorderYaml,
		},
		{
			Name:  "DRONSOLE_FOG_BASH",
			Value: opts.FogBash,
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
						"drone-device-id": opts.DeviceID,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            name,
							Image:           opts.Image,
							ImagePullPolicy: c.PullPolicy,
							Env:             droneContainerEnvs,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
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
	_, err := c.Clientset.CoreV1().Secrets(opts.Namespace).Create(ctx, droneSecret, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	} else if err != nil {
		return err
	}
	droneDeployment, err = c.Clientset.AppsV1().Deployments(opts.Namespace).Create(ctx, droneDeployment, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	} else if err != nil {
		return err
	}
	_, err = c.Clientset.CoreV1().Services(opts.Namespace).Create(ctx, droneService, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	}
	return err
}

func (c *Client) DeleteDrone(ctx context.Context, namespace, deviceID string) error {
	name := "drone-" + deviceID
	gracePeriod := int64(5)
	opts := metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod}
	var errs *multierror.Error
	err := c.Clientset.AppsV1().Deployments(namespace).Delete(ctx, name, opts)
	if !k8serrors.IsNotFound(err) {
		errs = multierror.Append(errs, err)
	}
	err = c.Clientset.CoreV1().Services(namespace).Delete(ctx, name+"-svc", opts)
	if !k8serrors.IsNotFound(err) {
		errs = multierror.Append(errs, err)
	}
	err = c.Clientset.CoreV1().Secrets(namespace).Delete(ctx, name+"-secret", opts)
	if !k8serrors.IsNotFound(err) {
		errs = multierror.Append(errs, err)
	}
	return errs.ErrorOrNil()
}

func (c *Client) GetDronePodName(ctx context.Context, namespace string, deviceID string) (string, error) {
	pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "drone-device-id=" + deviceID})
	if err != nil {
		return "", err
	}
	if len(pods.Items) != 1 {
		return "", ErrNoSuchDrone
	}
	return pods.Items[0].Name, nil
}

func (c *Client) GetDroneLogs(ctx context.Context, simulationName, droneID string) ([]byte, error) {
	pods, err := c.Clientset.CoreV1().Pods(simulationName).List(ctx, metav1.ListOptions{
		LabelSelector: "drone-device-id=" + droneID,
	})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) != 1 {
		return nil, ErrNoSuchDrone
	}
	return c.Clientset.CoreV1().Pods(simulationName).GetLogs(pods.Items[0].Name, &v1.PodLogOptions{
		Follow: false,
	}).DoRaw(ctx)
}

func (c *Client) GetDroneIdentityKey(ctx context.Context, simulationName, droneID string) ([]byte, error) {
	secret, err := c.Clientset.CoreV1().Secrets(simulationName).Get(ctx, "drone-"+droneID+"-secret", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret.Data["DRONE_IDENTITY_KEY"], nil
}

func (c *Client) CreateViewer(ctx context.Context, namespace, image, simCoordURL string) error {
	_, err := c.Clientset.AppsV1().Deployments(namespace).Get(ctx, "gzweb-dep", metav1.GetOptions{})
	if err == nil {
		// the deployment already exists
		return nil
	}

	portRange, err := c.getPortRange(ctx, namespace)
	if err != nil {
		return err
	}

	// get gzserver deployment
	gzserverDep, err := c.Clientset.AppsV1().Deployments(namespace).Get(ctx, "gzserver-dep", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("Unable to get gzserverwdep")
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
							ImagePullPolicy: c.PullPolicy,
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
							ImagePullPolicy: c.PullPolicy,
							VolumeMounts: []v1.VolumeMount{
								{
									MountPath: "/data",
									Name:      "gazebo-data-vol",
								},
							},
							Env: []v1.EnvVar{{
								Name:  "SIMULATION_COORDINATOR_URL",
								Value: simCoordURL,
							}, {
								Name:  "ENABLE_AUTHENTICATION",
								Value: "true",
							}},
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	gzwebDeployment, err = c.Clientset.AppsV1().Deployments(namespace).Create(ctx, gzwebDeployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating gzweb deployment %w", err)
	}

	gzwebService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gzweb-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:       "primary",
				Port:       portRange + gzwebPortOffset,
				TargetPort: intstr.FromInt(8080),
			}},
			Selector: map[string]string{"app": "gzweb-pod"},
			Type:     v1.ServiceTypeLoadBalancer,
		},
	}

	_, err = c.Clientset.CoreV1().Services(namespace).Create(ctx, gzwebService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating gzweb service %w", err)
	}
	return nil
}

func (c *Client) InitUpdateAgent(ctx context.Context, namespace string) error {
	false := false
	serviceAccount := &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "update-agent-account",
			Namespace: namespace,
		},
		AutomountServiceAccountToken: &false,
	}
	role := &rbac.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "update-agent-role",
			Namespace: namespace,
		},
		Rules: []rbac.PolicyRule{{
			APIGroups: []string{"", "apps"},
			Resources: []string{
				"namespaces", "deployments", "services", "pods",
				"pods/log", "pods/exec", "secrets", "configmaps",
			},
			Verbs: []string{"get", "watch", "list", "create", "delete"},
		}},
	}
	roleBinding := &rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "update-agent-binding",
			Namespace: namespace,
		},
		Subjects: []rbac.Subject{{
			Kind:      "ServiceAccount",
			Name:      "update-agent-account",
			Namespace: namespace,
		}},
		RoleRef: rbac.RoleRef{
			Kind:     "Role",
			Name:     "update-agent-role",
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	_, err := c.Clientset.RbacV1().Roles(namespace).Create(ctx, role, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = c.Clientset.RbacV1().RoleBindings(namespace).Create(ctx, roleBinding, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	_, err = c.Clientset.CoreV1().ServiceAccounts(namespace).Create(ctx, serviceAccount, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) CreateDroneOta(ctx context.Context, opts *CreateDroneOptions, otaProfileBytes []byte, imageUpdateAgent string) error {
	true := true
	name := fmt.Sprintf("drone-%s-%s", opts.DeviceID, parseImage(imageUpdateAgent))

	files := map[string][]byte{
		"communication_link.config":    []byte(opts.CommlinkYaml),
		"fog_env":                      []byte(opts.FogBash),
		"mission_data_recorder.config": []byte(opts.RecorderYaml),
		"ota-profile.yaml":             otaProfileBytes,
		"rsa_private.pem":              []byte(opts.PrivateKey),
	}

	data := wrapFiles(files)

	droneDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app":             name,
				"drone-device-id": opts.DeviceID,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":             name,
					"drone-device-id": opts.DeviceID,
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":             name,
						"drone-device-id": opts.DeviceID,
					},
				},
				Spec: v1.PodSpec{
					ServiceAccountName:           "update-agent-account",
					AutomountServiceAccountToken: &true,
					Volumes: []v1.Volume{
						enclaveVolume(name),
					},
					InitContainers: []v1.Container{
						enclaveInitializer(name, data),
					},
					Containers: []v1.Container{
						{
							Name:            name,
							Image:           imageUpdateAgent,
							ImagePullPolicy: c.PullPolicy,
							Args:            []string{"-namespace=" + opts.Namespace, "-device-id=" + opts.DeviceID},
							VolumeMounts: []v1.VolumeMount{
								enclaveMount(name),
							},
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := c.Clientset.AppsV1().Deployments(opts.Namespace).Create(ctx, droneDeployment, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	} else if err != nil {
		return err
	}

	return err
}

func enclaveVolume(name string) v1.Volume {
	return v1.Volume{
		Name: name + "-vol",
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
		},
	}
}

func enclaveInitializer(name string, data string) v1.Container {
	return v1.Container{
		Name:            name + "-init",
		Image:           "alpine:3.13",
		ImagePullPolicy: v1.PullIfNotPresent,
		Command:         []string{"sh", "-c", data},
		VolumeMounts: []v1.VolumeMount{
			{
				MountPath: "/enclave",
				Name:      name + "-vol",
				ReadOnly:  false,
			},
		},
	}
}

func enclaveMount(name string) v1.VolumeMount {
	return v1.VolumeMount{
		MountPath: "/enclave",
		Name:      name + "-vol",
	}
}

func wrapFiles(files map[string][]byte) string {
	cmd := ""
	for f, b := range files {
		cmd += fmt.Sprintf("echo \"%s\" > /enclave/%s;", string(b), f)
	}
	return cmd
}

// return image name without url and tag
func parseImage(image string) string {
	s1 := strings.Split(image, "/")
	nameTag := s1[len(s1)-1]
	s2 := strings.Split(nameTag, ":")
	return s2[0]
}
