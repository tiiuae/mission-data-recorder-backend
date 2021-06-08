package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/tiiuae/fleet-management/simulation-coordinator/pkg/kube"
)

func GetSimulationsHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	kube := kube.GetKube()
	namespaces, err := kube.CoreV1().Namespaces().List(c, metav1.ListOptions{LabelSelector: "dronsole-type=simulation"})
	if err != nil {
		writeError(w, "Could not get simulations", err, http.StatusInternalServerError)
		return
	}

	type resp struct {
		Name  string `json:"name"`
		Phase string `json:"phase"`
		Type  string `json:"type"`
	}
	response := make([]resp, len(namespaces.Items))
	for i, ns := range namespaces.Items {
		response[i] = resp{
			Name:  ns.Labels["dronsole-simulation-name"],
			Phase: fmt.Sprintf("%s", ns.Status.Phase),
			Type:  ns.Labels["dronsole-simulation-type"],
		}
	}
	writeJSON(w, response)
}

func getSimulationNameUniqueLength(c context.Context, kube *kubernetes.Clientset, name string) int {
	namespaces, err := kube.CoreV1().Namespaces().List(c, metav1.ListOptions{LabelSelector: "dronsole-type=simulation"})
	if err != nil {
		panic(err)
	}
	minLength := 1
	for _, ns := range namespaces.Items {
		for i := 1; i < 22; i++ {
			if !strings.HasPrefix(ns.Name, name[:i]) {
				if i > minLength {
					minLength = i
				}
				break
			}
		}
	}
	return minLength
}

func generateSimulationName(c context.Context, kube *kubernetes.Clientset) string {
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("sim-%s", uuid.New().String())
		l := getSimulationNameUniqueLength(c, kube, name)
		if l < 12 {
			// we found a short simulation name
			return name
		}
	}

	panic("Could not find unique name for simulation")
}
func CreateSimulationHandler(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	var request struct {
		World         string `json:"world"`
		Standalone    bool   `json:"standalone"`
		DataContainer string `json:"data_container"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	r.Body.Close()
	if err != nil {
		writeError(w, "Could not unmarshal simulation request", err, http.StatusInternalServerError)
		return
	}

	kube := kube.GetKube()
	name := generateSimulationName(c, kube)
	log.Printf("Creating simulation %s with world %s", name, request.World)
}
