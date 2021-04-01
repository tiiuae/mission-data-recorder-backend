package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/mux"
	"google.golang.org/api/cloudiot/v1"
	"gopkg.in/yaml.v3"
)

type urlGenerator struct {
	Bucket        string
	Account       string
	SigningKey    []byte
	ValidDuration time.Duration
}

func (g *urlGenerator) Generate(object, method string) (string, error) {
	url, err := storage.SignedURL(g.Bucket, object, &storage.SignedURLOptions{
		GoogleAccessID: g.Account,
		PrivateKey:     g.SigningKey,
		Method:         method,
		Expires:        timeNow().Add(g.ValidDuration),
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %w", err)
	}
	return url, nil
}

func configErr(err error) error {
	return fmt.Errorf("failed to load configuration: %w", err)
}

type configuration struct {
	Bucket           string        `yaml:"bucket"`
	Account          string        `yaml:"account"`
	PrivateKeyFile   string        `yaml:"privateKeyFile"`
	PrivateKey       []byte        `yaml:"-"`
	URLValidDuration time.Duration `yaml:"urlValidDuration"`
	Port             int           `yaml:"port"`
	GCP              gcpConfig     `yaml:"gcp"`
}

var config configuration

func loadConfig(configPath string) error {
	f, err := os.Open(configPath)
	if err != nil {
		return configErr(err)
	}
	defer f.Close()
	if err := yaml.NewDecoder(f).Decode(&config); err != nil {
		return configErr(err)
	}
	config.PrivateKey, err = os.ReadFile(config.PrivateKeyFile)
	if err != nil {
		return configErr(fmt.Errorf("error loading private key: %w", err))
	}
	return nil
}

func urlGeneratorFromConfig(config *configuration) *urlGenerator {
	return &urlGenerator{
		Bucket:        config.Bucket,
		Account:       config.Account,
		SigningKey:    config.PrivateKey,
		ValidDuration: config.URLValidDuration,
	}
}

func newObjectName(deviceID string) string {
	return fmt.Sprintf("%s/%d", deviceID, timeNow().Unix())
}

func urlGeneratorHandler(gen *urlGenerator, gcp gcpAPI) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const authPrefixLen = len("Bearer ")
		if len(auth) < authPrefixLen {
			writeErrMsg(rw, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}
		deviceID, err := validateJWT(r.Context(), gcp, auth[authPrefixLen:])
		if err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusForbidden, "forbidden")
			return
		}
		signedURL, err := gen.Generate(newObjectName(deviceID), "PUT")
		if err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusInternalServerError, "something went wrong")
			return
		}
		writeJSON(rw, jsonObj{"url": signedURL})
	})
}

func run() int {
	configPath := flag.String("config", "", "(required) config file path")
	flag.Parse()
	if *configPath == "" {
		log.Println("usage:", os.Args[0], "[flags]")
		flag.PrintDefaults()
		return 1
	}
	err := loadConfig(*configPath)
	if err != nil {
		log.Println(err)
		return 1
	}
	gen := urlGeneratorFromConfig(&config)

	config.GCP.iotService, err = cloudiot.NewService(context.Background())
	if err != nil {
		log.Println(err)
		return 1
	}

	r := mux.NewRouter()
	r.Use(requestLoggerMiddleware)
	r.Use(recoverPanicMiddleware)
	r.NotFoundHandler = notFoundHandler()
	r.MethodNotAllowedHandler = methodNotAllowedHandler()
	r.Path("/generate-url").Methods("POST").Handler(urlGeneratorHandler(gen, &config.GCP))

	log.Println("listening on port", config.Port)
	http.ListenAndServe(":"+strconv.Itoa(config.Port), r)
	return 0
}

func main() {
	os.Exit(run())
}
