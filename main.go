package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/mux"
	"github.com/spf13/pflag"
	"github.com/tiiuae/fleet-management/mission-data-recorder-backend/configloader"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudiot/v1"
	"google.golang.org/api/option"
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

func (g *urlGenerator) Generate(tenantID, deviceID, name, method string) (string, error) {
	if name == "" {
		name = generateBagName()
	}
	name = path.Join(g.Prefix+tenantID, deviceID, name)
	url, err := storage.SignedURL(g.Bucket, name, &storage.SignedURLOptions{
		GoogleAccessID: g.Account,
		PrivateKey:     g.SigningKey,
		Method:         method,
		Expires:        timeNow().Add(g.ValidDuration),
		Scheme:         storage.SigningSchemeV2,
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
	Bucket            string        `config:"bucket"`
	Account           string        `config:"account"`
	PrivateKeyFile    string        `config:"privateKeyFile"`
	URLValidDuration  time.Duration `config:"urlValidDuration"`
	Port              int           `config:"port"`
	GCP               gcpConfig     `config:"gcp"`
	LocalDir          string        `config:"fileStorageDirectory"`
	Host              string        `config:"host"`
	DataObjectPrefix  string        `config:"dataObjectPrefix"`
	DisableValidation bool          `config:"disableValidation"`
	DefaultTenantID   string        `config:"defaultTenantID"`

	privateKey      []byte
	jsonCredentials []byte
}

func loadConfig() (config *configuration, err error) {
	config = &configuration{
		DefaultTenantID: "fleet-registry",
	}
	loader := configloader.New()
	loader.Args = os.Args
	loader.EnvPrefix = "MISSION_DATA_RECORDER_BACKEND"
	if err := loader.Load(config); err != nil {
		var f configloader.FatalErr
		if errors.As(err, &f) {
			return nil, err
		} else if errors.Is(err, pflag.ErrHelp) {
			return nil, nil
		}
		log.Println("during config loading:", err)
	}
	if config.LocalDir == "" {
		config.jsonCredentials, err = os.ReadFile(config.PrivateKeyFile)
		if err != nil {
			return nil, configErr(fmt.Errorf("failed to read private key: %w", err))
		}
		keyConfig, err := google.JWTConfigFromJSON(config.jsonCredentials)
		if err != nil {
			return nil, configErr(err)
		}
		config.privateKey = keyConfig.PrivateKey
	}
	if config.Host == "" {
		config.Host = "http://localhost:" + strconv.Itoa(config.Port)
	}
	return config, nil
}

func urlGeneratorFromConfig(config *configuration) *urlGenerator {
	g := &urlGenerator{
		Bucket:        config.Bucket,
		Account:       config.Account,
		SigningKey:    config.privateKey,
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

func signedURLGeneratorHandler(config *configuration, gcp gcpAPI) http.Handler {
	gen := urlGeneratorFromConfig(config)
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
		if config.DisableValidation {
			claims, err = getClaimsWithoutValidation(rawToken)
		} else {
			claims, err = validateJWT(r.Context(), gcp, config.DefaultTenantID, rawToken)
		}
		if err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusForbidden, "forbidden")
			return
		}
		signedURL, err := gen.Generate(
			claims.TenantID,
			claims.DeviceID,
			claims.BagName,
			"PUT",
		)
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
				"%s/upload?tenant=%s&device=%s&bagName=%s",
				host,
				url.QueryEscape(claims.TenantID),
				url.QueryEscape(claims.DeviceID),
				url.QueryEscape(claims.BagName),
			),
		})
	})
}

var pathSegmentSanitizer = strings.NewReplacer("..", "_", "/", "_")

func receiveUploadHandler(dirPath, defaultTenantID string) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			tenant = defaultTenantID
		}
		device := r.URL.Query().Get("device")
		if device == "" {
			writeErrMsg(rw, http.StatusBadRequest, "parameter 'device' is missing")
			return
		}
		dir := filepath.Join(
			dirPath,
			pathSegmentSanitizer.Replace(tenant),
			pathSegmentSanitizer.Replace(device),
		)
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
	config, err := loadConfig()
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
			option.WithCredentialsJSON(config.jsonCredentials),
		)
		if err != nil {
			log.Println(err)
			return 1
		}
		urlGenHandler = signedURLGeneratorHandler(config, &config.GCP)
	} else {
		urlGenHandler = localURLGeneratorHandler(config.Host)
		r.Path("/upload").Methods("PUT").Handler(receiveUploadHandler(
			config.LocalDir,
			config.DefaultTenantID,
		))
	}
	r.Path("/generate-url").Methods("POST").Handler(urlGenHandler)

	log.Println("listening on port", config.Port)
	http.ListenAndServe(":"+strconv.Itoa(config.Port), r)
	return 0
}

func main() {
	os.Exit(run())
}
