package kube

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/oauth2/google"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

var DefaultPullPolicy = v1.PullIfNotPresent

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
	ErrNoSuchDrone = errors.New("kube: no such drone")
	ErrDroneExists = errors.New("drone already exists")
)

const (
	globalPortRangeStart  = 38400
	portRangeSize         = 5
	videoServerPortOffset = 0
	gzwebPortOffset       = 1
	mqttServerPortOffset  = 2
)

func CopySecret(ctx context.Context, fromNamespace, fromName, toNamespace, toName string, clientset *kubernetes.Clientset) error {
	secret, err := clientset.CoreV1().Secrets(fromNamespace).Get(ctx, fromName, metav1.GetOptions{})
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
	_, err = clientset.CoreV1().Secrets(toNamespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func getPortRange(ctx context.Context, namespace string, clientset *kubernetes.Clientset) (int32, error) {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	p, err := strconv.ParseInt(ns.ObjectMeta.Annotations["dronsole-port-range-start"], 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(p), nil
}

func getFirstFreePortRange(ctx context.Context, clientset *kubernetes.Clientset) (int, error) {
	nss, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
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

func CreateNamespace(ctx context.Context, name, id string, simType SimulationType, clientset *kubernetes.Clientset) (*v1.Namespace, error) {
	port, err := getFirstFreePortRange(ctx, clientset)
	if port == 0 {
		return nil, fmt.Errorf("failed to get a free port range: %w", err)
	}
	if err != nil {
		log.Println("errors occurred when searching for a free port range:", err)
	}
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"dronsole-type":            "simulation",
				"dronsole-simulation-type": string(simType),
			},
			Annotations: map[string]string{
				"dronsole-expiration-timestamp": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
				"dronsole-simulation-id":        id,
				"dronsole-port-range-start":     strconv.Itoa(port),
			},
		},
	}
	return clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
}

func CreateGZServer(ctx context.Context, namespace, gzserverImage, dataImage string, gpuMode SimulationGPUMode, cloudMode bool, clientset *kubernetes.Clientset) error {
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
	_, err := clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	return err
}

func CreateMQTT(c context.Context, namespace string, image string, loadBalancer bool, clientset *kubernetes.Clientset) error {

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
							ImagePullPolicy: DefaultPullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(c, &mqttDeployment, metav1.CreateOptions{})
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

	_, err = clientset.CoreV1().Services(namespace).Create(c, &mqttService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating mqtt-server service %w", err)
	}
	if loadBalancer {
		portRange, err := getPortRange(c, namespace, clientset)
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
		_, err = clientset.CoreV1().Services(namespace).Create(c, publicService, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("Error creating mqtt-server public service %w", err)
		}
	}
	return nil
}

func CreateMissionControl(c context.Context, namespace string, image string, clientset *kubernetes.Clientset) error {

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
							ImagePullPolicy: DefaultPullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(c, &missionControlDeployment, metav1.CreateOptions{})
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

	_, err = clientset.CoreV1().Services(namespace).Create(c, &missionControlService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating mission-control service %v", err)
		return err
	}

	return nil
}

func CreateVideoServer(c context.Context, namespace, image, cert, key string, clientset *kubernetes.Clientset) error {
	portRange, err := getPortRange(c, namespace, clientset)
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
	_, err = clientset.CoreV1().Secrets(namespace).Create(c, secret, metav1.CreateOptions{})
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
							ImagePullPolicy: DefaultPullPolicy,
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

	_, err = clientset.AppsV1().Deployments(namespace).Create(c, &videoServerDeployment, metav1.CreateOptions{})
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

	_, err = clientset.CoreV1().Services(namespace).Create(c, &videoServerService, metav1.CreateOptions{})
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
	_, err = clientset.CoreV1().Services(namespace).Create(c, publicService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating video-server service %w", err)
	}
	return nil
}

func CreateVideoMultiplexer(c context.Context, namespace string, image string, clientset *kubernetes.Clientset) error {

	videoServerDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-multiplexer-dep",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "video-multiplexer-pod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "video-multiplexer-pod",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "video-multiplexer",
							Image:           image,
							Args:            []string{"-mqtt", "tcp://mqtt-server-svc:8883", "-rtsp", "video-server-svc:8554", "-test"},
							ImagePullPolicy: DefaultPullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(c, &videoServerDeployment, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating video-multiplexer deployment %v", err)
		return err
	}

	videoServerService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "video-multiplexer-svc",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port: 8084,
			}},
			Selector: map[string]string{"app": "video-multiplexer-pod"},
			Type:     v1.ServiceTypeClusterIP,
		},
	}

	_, err = clientset.CoreV1().Services(namespace).Create(c, &videoServerService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating video-multiplexer service %v", err)
		return err
	}

	return nil
}

func CreateWebBackend(c context.Context, namespace string, image string, clientset *kubernetes.Clientset) error {

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
							ImagePullPolicy: DefaultPullPolicy,
						},
					},
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: "dockerconfigjson",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(c, &videoServerDeployment, metav1.CreateOptions{})
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

	_, err = clientset.CoreV1().Services(namespace).Create(c, &videoServerService, metav1.CreateOptions{})
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

func CreateMissionDataRecorderBackend(ctx context.Context, clientset *kubernetes.Clientset, opts *MissionDataRecorderBackendOptions) error {
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
		_, err = clientset.CoreV1().Secrets(opts.Namespace).Create(ctx, secret, metav1.CreateOptions{})
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
							ImagePullPolicy: DefaultPullPolicy,
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
	_, err := clientset.CoreV1().ConfigMaps(opts.Namespace).Create(ctx, config, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = clientset.AppsV1().Deployments(opts.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = clientset.CoreV1().Services(opts.Namespace).Create(ctx, service, metav1.CreateOptions{})
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
}

func CreateDrone(ctx context.Context, clientset *kubernetes.Clientset, opts *CreateDroneOptions) error {
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
							ImagePullPolicy: DefaultPullPolicy,
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
	_, err := clientset.CoreV1().Secrets(opts.Namespace).Create(ctx, droneSecret, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	} else if err != nil {
		return err
	}
	droneDeployment, err = clientset.AppsV1().Deployments(opts.Namespace).Create(ctx, droneDeployment, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	} else if err != nil {
		return err
	}
	_, err = clientset.CoreV1().Services(opts.Namespace).Create(ctx, droneService, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return ErrDroneExists
	}
	return err
}

func DeleteDrone(ctx context.Context, namespace, deviceID string, clientset *kubernetes.Clientset) error {
	name := "drone-" + deviceID
	gracePeriod := int64(5)
	opts := metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod}
	var errs *multierror.Error
	err := clientset.AppsV1().Deployments(namespace).Delete(ctx, name, opts)
	if !k8serrors.IsNotFound(err) {
		errs = multierror.Append(errs, err)
	}
	err = clientset.CoreV1().Services(namespace).Delete(ctx, name+"-svc", opts)
	if !k8serrors.IsNotFound(err) {
		errs = multierror.Append(errs, err)
	}
	err = clientset.CoreV1().Secrets(namespace).Delete(ctx, name+"-secret", opts)
	if !k8serrors.IsNotFound(err) {
		errs = multierror.Append(errs, err)
	}
	return errs.ErrorOrNil()
}

func GetDronePodName(c context.Context, namespace string, deviceID string, clientset *kubernetes.Clientset) (string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(c, metav1.ListOptions{LabelSelector: "drone-device-id=" + deviceID})
	if err != nil {
		return "", err
	}
	if len(pods.Items) != 1 {
		return "", ErrNoSuchDrone
	}
	return pods.Items[0].Name, nil
}

func CreateViewer(c context.Context, namespace, image, simCoordURL string, kube *kubernetes.Clientset) error {
	_, err := kube.AppsV1().Deployments(namespace).Get(c, "gzweb-dep", metav1.GetOptions{})
	if err == nil {
		// the deployment already exists
		return nil
	}

	portRange, err := getPortRange(c, namespace, kube)
	if err != nil {
		return err
	}

	// get gzserver deployment
	gzserverDep, err := kube.AppsV1().Deployments(namespace).Get(c, "gzserver-dep", metav1.GetOptions{})
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
							ImagePullPolicy: DefaultPullPolicy,
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
							ImagePullPolicy: DefaultPullPolicy,
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

	gzwebDeployment, err = kube.AppsV1().Deployments(namespace).Create(c, gzwebDeployment, metav1.CreateOptions{})
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

	_, err = kube.CoreV1().Services(namespace).Create(c, gzwebService, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("Error creating gzweb service %w", err)
	}
	return nil
}
