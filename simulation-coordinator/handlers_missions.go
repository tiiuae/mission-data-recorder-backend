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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

var globalMissionsURL *url.URL

func init() {
	var err error
	globalMissionsURL, err = url.Parse("https://missions.webapi.sacplatform.com")
	if err != nil {
		panic(err)
	}
}

func getMissionsURL(ctx context.Context, sim string) (*url.URL, error) {
	simType, err := getSimulationType(ctx, sim)
	if err != nil {
		return nil, err
	}
	switch simType {
	case simTypeGlobal:
		return globalMissionsURL, nil
	case simTypeStandalone:
		return &url.URL{
			Scheme: "http",
			Host:   "mission-control-svc." + sim + ":8082",
		}, nil
	default:
		panic("invalid simulation type: " + string(simType))
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
	missionsURL, err := getMissionsURL(ctx, simulationName)
	if err != nil {
		return nil, err
	}
	var resp []mission
	url := missionsURL.String() + "/missions"
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
	missionsURL, err := getMissionsURL(r.Context(), simulationName)
	if err != nil {
		writeServerError(w, "failed to get missions", err)
		return
	}
	writeJSON(w, obj{
		"mission_controller_hostname": missionsURL.Hostname(),
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
	missionsURL, err := getMissionsURL(r.Context(), simulationName)
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "failed to create mission", err)
		return
	}
	url := missionsURL.String() + "/missions"
	if err = postJSON(r.Context(), url, nil, &req); err != nil {
		writeServerError(w, "failed to create mission", err)
		return
	}
	writeJSON(w, obj{"slug": req.Slug})
}

func deleteMissionHandler(w http.ResponseWriter, r *http.Request) {
	params := httprouter.ParamsFromContext(r.Context())
	simulationName := params.ByName("simulationName")
	missionSlug := params.ByName("missionSlug")

	missionsURL, err := getMissionsURL(r.Context(), simulationName)
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "could not generate mission slug", err)
		return
	}
	url := missionsURL.String() + "/missions/" + missionSlug
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

	missionsURL, err := getMissionsURL(r.Context(), simulationName)
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "could not generate mission slug", err)
		return
	}
	url := missionsURL.String() + "/missions/" + missionSlug + "/drones"
	if err = postJSON(r.Context(), url, nil, &req); err != nil {
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
	missionsURL, err := getMissionsURL(r.Context(), simulationName)
	if k8serrors.IsNotFound(err) {
		writeNotFound(w, "simulation doesn't exist", err)
		return
	} else if err != nil {
		writeServerError(w, "could not generate mission slug", err)
		return
	}
	url := missionsURL.String() + "/missions/" + missionSlug + "/backlog"
	if err = postJSON(r.Context(), url, nil, &req); err != nil {
		writeServerError(w, "failed to assign drone to mission", err)
		return
	}
}