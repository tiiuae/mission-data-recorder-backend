package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

type urlGenerator struct {
	Bucket        string
	Account       string
	PrivateKey    []byte
	ValidDuration time.Duration
}

func (g *urlGenerator) Generate(object, method string) (string, error) {
	url, err := storage.SignedURL(g.Bucket, object, &storage.SignedURLOptions{
		GoogleAccessID: g.Account,
		PrivateKey:     g.PrivateKey,
		Method:         method,
		Expires:        time.Now().Add(g.ValidDuration),
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
	Bucket           string              `yaml:"bucket"`
	Account          string              `yaml:"account"`
	PrivateKeyFile   string              `yaml:"privateKeyFile"`
	PrivateKey       []byte              `yaml:"-"`
	URLValidDuration time.Duration       `yaml:"urlValidDuration"`
	Port             int                 `yaml:"port"`
	APIKeys          []string            `yaml:"apiKeys"`
	APIKeySet        map[string]struct{} `yaml:"-"`
	FilePrefix       string              `yaml:"filePrefix"`
}

func loadConfig(configPath string) (*configuration, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, configErr(err)
	}
	defer f.Close()
	var config configuration
	if err := yaml.NewDecoder(f).Decode(&config); err != nil {
		return nil, configErr(err)
	}
	config.APIKeySet = map[string]struct{}{}
	for _, key := range config.APIKeys {
		config.APIKeySet[key] = struct{}{}
	}
	config.PrivateKey, err = os.ReadFile(config.PrivateKeyFile)
	if err != nil {
		return nil, configErr(fmt.Errorf("error loading private key: %w", err))
	}
	if config.FilePrefix == "" {
		config.FilePrefix = "mission-data"
	}
	return &config, nil
}

func urlGeneratorFromConfig(config *configuration) *urlGenerator {
	return &urlGenerator{
		Bucket:        config.Bucket,
		Account:       config.Account,
		PrivateKey:    config.PrivateKey,
		ValidDuration: config.URLValidDuration,
	}
}

type nameGenerator struct {
	lock    sync.Mutex
	counter int
	prefix  string
}

func newNameGenerator(prefix string) *nameGenerator {
	return &nameGenerator{
		prefix: prefix,
	}
}

func (g *nameGenerator) GetName() string {
	g.lock.Lock()
	defer g.lock.Unlock()
	g.counter++
	return g.prefix + strconv.Itoa(g.counter)
}

func httpGeneratorHandler(config *configuration, gen *urlGenerator) http.Handler {
	nameGen := newNameGenerator(config.FilePrefix)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		apiKey := r.URL.Query().Get("apikey")
		if _, ok := config.APIKeySet[apiKey]; !ok {
			writeErrMsg(rw, http.StatusForbidden, "forbidden")
			return
		}
		signedURL, err := gen.Generate(nameGen.GetName(), "PUT")
		if err != nil {
			log.Println(err)
			writeErrMsg(rw, http.StatusInternalServerError, "something went wrong")
			return
		}
		log.Println(signedURL)
		writeJSON(rw, jsonObj{"url": signedURL})
	})
}

func printErr(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
}

func main() {
	configPath := flag.String("config", "", "(required) config file path")
	flag.Parse()
	if *configPath == "" {
		printErr("usage:", os.Args[0], "[flags]")
		flag.PrintDefaults()
		return
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		printErr(err)
		return
	}
	gen := urlGeneratorFromConfig(config)

	r := mux.NewRouter()
	r.Use(requestLoggerMiddleware)
	r.Use(recoverPanicMiddleware)
	r.NotFoundHandler = notFoundHandler()
	r.NotFoundHandler = methodNotAllowedHandler()
	r.Path("/generate-url").Methods("GET").Handler(httpGeneratorHandler(config, gen))

	log.Println("listening on port", config.Port)
	http.ListenAndServe(":"+strconv.Itoa(config.Port), r)
}
