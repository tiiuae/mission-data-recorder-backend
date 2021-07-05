package kube

import (
	"context"
	"log"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func CreateMQTT(c context.Context, namespace string, image string, clientset *kubernetes.Clientset) error {

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
							ImagePullPolicy: v1.PullIfNotPresent,
						},
					},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(c, &mqttDeployment, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating mqtt-server deployment %v", err)
		return err
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
			Type:     v1.ServiceTypeLoadBalancer,
		},
	}

	_, err = clientset.CoreV1().Services(namespace).Create(c, &mqttService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating mqtt-server service %v", err)
		return err
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
							ImagePullPolicy: v1.PullIfNotPresent,
						},
					},
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
			Type:     v1.ServiceTypeLoadBalancer,
		},
	}

	_, err = clientset.CoreV1().Services(namespace).Create(c, &missionControlService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating mission-control service %v", err)
		return err
	}

	return nil
}

func CreateVideoServer(c context.Context, namespace string, image string, clientset *kubernetes.Clientset) error {

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
							ImagePullPolicy: v1.PullIfNotPresent,
						},
					},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(c, &videoServerDeployment, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating video-server deployment %v", err)
		return err
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
			Type:     v1.ServiceTypeLoadBalancer,
		},
	}

	_, err = clientset.CoreV1().Services(namespace).Create(c, &videoServerService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating video-server service %v", err)
		return err
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
							ImagePullPolicy: v1.PullIfNotPresent,
						},
					},
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
			Type:     v1.ServiceTypeLoadBalancer,
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
							ImagePullPolicy: v1.PullIfNotPresent,
						},
					},
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
			Type:     v1.ServiceTypeLoadBalancer,
		},
	}

	_, err = clientset.CoreV1().Services(namespace).Create(c, &videoServerService, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error creating web-backend service %v", err)
		return err
	}

	return nil

}
