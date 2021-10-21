package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/mux"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudiot/v1"
	"google.golang.org/api/option"
	"gopkg.in/yaml.v3"
)

const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

func generateBagName() string {
	return timeNow().UTC().Format(timeFormat) + ".db3"
}

type urlGenerator struct {
	Bucket        string
	Account       string
	SigningKey    []byte
	ValidDuration time.Duration
	Prefix        string
}

func (g *urlGenerator) Generate(deviceID, name, method string) (string, error) {
	if name == "" {
		name = generateBagName()
	}
	name = g.Prefix + deviceID + "/" + name
	url, err := storage.SignedURL(g.Bucket, name, &storage.SignedURLOptions{
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
	Bucket            string        `yaml:"bucket"`
	Account           string        `yaml:"account"`
	PrivateKeyFile    string        `yaml:"privateKeyFile"`
	PrivateKey        []byte        `yaml:"-"`
	JSONCredentials   []byte        `yaml:"-"`
	URLValidDuration  time.Duration `yaml:"urlValidDuration"`
	Port              int           `yaml:"port"`
	GCP               gcpConfig     `yaml:"gcp"`
	LocalDir          string        `yaml:"fileStorageDirectory"`
	Host              string        `yaml:"host"`
	DataObjectPrefix  string        `yaml:"dataObjectPrefix"`
	DisableValidation bool          `yaml:"disableValidation"`
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
	if config.LocalDir == "" {
		config.JSONCredentials, err = os.ReadFile(config.PrivateKeyFile)
		if err != nil {
			return configErr(err)
		}
		keyConfig, err := google.JWTConfigFromJSON(config.JSONCredentials)
		if err != nil {
			return configErr(err)
		}
		config.PrivateKey = keyConfig.PrivateKey
	}
	if config.Host == "" {
		config.Host = "http://localhost:" + strconv.Itoa(config.Port)
	}
	return nil
}

func urlGeneratorFromConfig(config *configuration) *urlGenerator {
	g := &urlGenerator{
		Bucket:        config.Bucket,
		Account:       config.Account,
		SigningKey:    config.PrivateKey,
		ValidDuration: config.URLValidDuration,
		Prefix:        strings.Trim(config.DataObjectPrefix, "/"),
	}
	if g.Prefix != "" {
		g.Prefix += "/"
	}
	return g
}

func readAuthJWT(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const authPrefixLen = len("Bearer ")
	if len(auth) < authPrefixLen {
		return ""
	}
	return auth[authPrefixLen:]
}

func internalServerErr(rw http.ResponseWriter) {
	writeErrMsg(rw, http.StatusInternalServerError, "something went wrong")
}

func signedURLGeneratorHandler(gen *urlGenerator, gcp gcpAPI, disableValidation bool) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rawToken := readAuthJWT(r)
		if rawToken == "" {
			writeErrMsg(rw, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}
		var (
			claims *jwtClaims
			err    error
		)
		if disableValidation {
			claims, err = getClaimsWithoutValidation(rawToken)
		} else {
			claims, err = validateJWT(r.Context(), gcp, rawToken)
		}
		if err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusForbidden, "forbidden")
			return
		}
		signedURL, err := gen.Generate(claims.DeviceID, claims.BagName, "PUT")
		if err != nil {
			log.Println(err)
			internalServerErr(rw)
			return
		}
		writeJSON(rw, jsonObj{"url": signedURL})
	})
}

func localURLGeneratorHandler(host string) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rawToken := readAuthJWT(r)
		if rawToken == "" {
			writeErrMsg(rw, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}
		claims, err := getClaimsWithoutValidation(rawToken)
		if err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusForbidden, "forbidden")
			return
		}
		writeJSON(rw, jsonObj{
			"url": fmt.Sprintf(
				"%s/upload?device=%s&bagName=%s",
				host,
				url.QueryEscape(claims.DeviceID),
				url.QueryEscape(claims.BagName),
			),
		})
	})
}

var pathSegmentSanitizer = strings.NewReplacer("..", "_", "/", "_")

func receiveUploadHandler(dirPath string) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		device := r.URL.Query().Get("device")
		if device == "" {
			writeErrMsg(rw, http.StatusBadRequest, "parameter 'device' is missing")
			return
		}
		dir := filepath.Join(dirPath, pathSegmentSanitizer.Replace(device))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Println(err)
			internalServerErr(rw)
			return
		}
		bagName := pathSegmentSanitizer.Replace(r.URL.Query().Get("bagName"))
		if bagName == "" {
			bagName = generateBagName()
		}
		f, err := os.Create(filepath.Join(dir, bagName))
		if err != nil {
			log.Println(err)
			internalServerErr(rw)
			return
		}
		defer f.Close()
		if _, err := io.Copy(f, r.Body); err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusBadRequest, "failed to store the file")
			return
		}
		rw.WriteHeader(http.StatusOK)
	})
}

func healthCheck(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
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

	r := mux.NewRouter()
	r.Use(requestLoggerMiddleware)
	r.Use(recoverPanicMiddleware)
	r.NotFoundHandler = notFoundHandler()
	r.MethodNotAllowedHandler = methodNotAllowedHandler()
	r.Path("/healthz").Methods("GET").HandlerFunc(healthCheck)

	var urlGenHandler http.Handler
	if config.LocalDir == "" {
		config.GCP.iotService, err = cloudiot.NewService(
			context.Background(),
			option.WithCredentialsJSON(config.JSONCredentials),
		)
		if err != nil {
			log.Println(err)
			return 1
		}
		urlGenHandler = signedURLGeneratorHandler(
			urlGeneratorFromConfig(&config),
			&config.GCP,
			config.DisableValidation,
		)
	} else {
		urlGenHandler = localURLGeneratorHandler(config.Host)
		r.Path("/upload").Methods("PUT").Handler(receiveUploadHandler(config.LocalDir))
	}
	r.Path("/generate-url").Methods("POST").Handler(urlGenHandler)

	log.Println("listening on port", config.Port)
	http.ListenAndServe(":"+strconv.Itoa(config.Port), r)
	return 0
}

func main() {
	os.Exit(run())
}
