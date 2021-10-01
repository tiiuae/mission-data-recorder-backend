package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
	"github.com/pkg/errors"
	"github.com/tiiuae/fleet-management/simulation-coordinator/kube"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

func getMissionsURL(ctx context.Context, simulationName string, path string) (string, error) {
	simType, err := client.GetSimulationType(ctx, simulationName)
	if err != nil {
		return "", err
	}

	registryID := getRegistryID(simType, simulationName)

	switch simType {
	case kube.SimulationGlobal:
		return fmt.Sprintf("%s%s?tid=%s", missionControlURL, path, registryID), nil
	case kube.SimulationStandalone:
		return fmt.Sprintf("http://mission-control-svc.%s:8082%s?tid=%s", simulationName, path, registryID), nil
	default:
		return "", errors.Errorf("invalid simulation type: " + string(simType))
	}
}

var colors = []string{
	"black",
	"blue",
	"crimson",
	"gray",
	"green",
	"indigo",
	"orange",
	"violet",
	"white",
	"yellow",
}

func generateMissionSlug(c context.Context, simulationName string) (string, error) {
	missions, err := getMissions(c, simulationName)
	if err != nil {
		return "", err
	}
	availableColors := make([]string, 0)
	for _, color := range colors {
		available := true
		for _, mission := range missions {
			if strings.HasPrefix(mission.Slug, color) {
				available = false
				break
			}
		}
		if !available {
			continue
		}
		availableColors = append(availableColors, color)
	}
	if len(availableColors) != 0 {
		// still free colors
		return availableColors[rand.Intn(len(availableColors))], nil
	}

	// no free colors
	for i := 0; i < 20; i++ {
		color := colors[rand.Intn(len(colors))]
		slug := fmt.Sprintf("%s%s", color, strings.Split(uuid.New().String(), "-")[3])
		used := false
		for _, mission := range missions {
			if mission.Slug == slug {
				used = true
				break
			}
		}
		if used {
			continue
		}
		return slug, nil
	}

	return "", fmt.Errorf("Could not generate unique mission slug in 20 tries")
}

type mission struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func getMissions(ctx context.Context, simulationName string) ([]mission, error) {
	url, err := getMissionsURL(ctx, simulationName, "/missions")
	if err != nil {
		return nil, err
	}
	var resp []mission
	if err = getJSON(ctx, url, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func getMissionsHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")

	resp, err := getMissions(r.Context(), simulationName)
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", nil)
		return
	} else if err != nil {
		writeServerError(w, "failed to get missions", err)
		return
	}
	missionsURL, err := getMissionsURL(r.Context(), simulationName, "")
	if err != nil {
		writeServerError(w, "failed to get missions", err)
		return
	}
	url, err := url.Parse(missionsURL)
	if err != nil {
		writeServerError(w, "failed to get missions", err)
		return
	}

	writeJSON(w, obj{
		"mission_controller_hostname": url.Hostname(),
		"missions":                    resp,
	})
}

func createMissionHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")

	var req struct {
		Slug           string   `json:"slug"`
		Name           string   `json:"name"`
		AllowedSSHKeys []string `json:"allowed_ssh_keys"`
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		writeInvalidJSON(w, err)
		return
	}
	if req.Slug == "" {
		req.Slug, err = generateMissionSlug(r.Context(), simulationName)
		if k8serrors.IsNotFound(err) {
			writeNotFound(w, "simulation doesn't exist", err)
			return
		} else if err != nil {
			writeServerError(w, "could not generate mission slug", err)
			return
		}
	}
	if req.Name == "" {
		req.Name = req.Slug
	}
	url, err := getMissionsURL(r.Context(), simulationName, "/missions")
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "failed to create mission", err)
		return
	}
	if err = postJSON(r.Context(), url, nil, nil, &req); err != nil {
		writeServerError(w, "failed to create mission", err)
		return
	}
	writeJSON(w, obj{"slug": req.Slug})
}

func deleteMissionHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	missionSlug := params.ByName("missionSlug")

	url, err := getMissionsURL(r.Context(), simulationName, "/missions/"+missionSlug)
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "could not generate mission slug", err)
		return
	}
	if err = deleteJSON(r.Context(), url, nil, nil); err != nil {
		writeServerError(w, "failed to delete mission", err)
		return
	}
}

func assignDroneHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	missionSlug := params.ByName("missionSlug")
	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidJSON(w, err)
		return
	}

	url, err := getMissionsURL(r.Context(), simulationName, "/missions/"+missionSlug+"/drones")
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "could not generate mission slug", err)
		return
	}
	if err = postJSON(r.Context(), url, nil, nil, &req); err != nil {
		writeServerError(w, "failed to assign drone to mission", err)
		return
	}
}

func addBacklogItem(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	missionSlug := params.ByName("missionSlug")

	var req interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidJSON(w, err)
		return
	}
	url, err := getMissionsURL(r.Context(), simulationName, "/missions/"+missionSlug+"/backlog")
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "could not generate mission slug", err)
		return
	}
	if err = postJSON(r.Context(), url, nil, nil, &req); err != nil {
		writeServerError(w, "failed to assign drone to mission", err)
		return
	}
}
