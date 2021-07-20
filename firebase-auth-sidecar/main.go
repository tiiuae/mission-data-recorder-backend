package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
)

var (
	port            = 9000
	targetURL       = ""
	ignoreAuthPaths = "/healthz"
)

var (
	targetURLProxy *httputil.ReverseProxy
	authClient     *auth.Client
)

func init() {
	flag.IntVar(&port, "port", port, "Port to listen to")
	flag.StringVar(&targetURL, "target-url", targetURL, "URL of service where authenticated requests are forwarded to")
	flag.StringVar(&ignoreAuthPaths, "ignore-auth-paths", ignoreAuthPaths, "Comma-separated list of paths for which credentials are not checked. If a path ends in '/', then all subpaths are ignored as well as the path itself.")
}

func authenticateHandler(c *gin.Context) {
	idToken := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if idToken == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "missing Authorization header",
		})
		return
	}
	_, err := authClient.VerifyIDTokenAndCheckRevoked(c.Request.Context(), idToken)
	if err != nil {
		log.Println("Failed to authenticate:", err)
		message := "unauthorized"
		if auth.IsIDTokenExpired(err) {
			message = "expired"
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": message,
		})
		return
	}
	targetURLProxy.ServeHTTP(c.Writer, c.Request)
}

func errorMiddleware(c *gin.Context) {
	c.Next()
	for _, err := range c.Errors {
		log.Println(err)
	}
	if !c.Writer.Written() && !isSuccess(c.Writer.Status()) {
		c.Header("Content-Length", "")
		c.JSON(c.Writer.Status(), gin.H{
			"error": http.StatusText(c.Writer.Status()),
		})
	}
}

func isSuccess(code int) bool {
	return 200 <= code && code < 300
}

func run() int {
	gin.SetMode(gin.ReleaseMode)
	flag.Parse()
	if targetURL == "" {
		log.Println("Flag -target-url is required")
		return 1
	}
	targetURL, err := url.Parse(targetURL)
	if err != nil {
		log.Println("Invalid target URL:", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	firebaseApp, err := firebase.NewApp(ctx, nil)
	if err != nil {
		log.Println("Failed to create firebase app:", err)
		return 1
	}
	authClient, err = firebaseApp.Auth(ctx)
	if err != nil {
		log.Println("Failed to create firebase client:", err)
		return 1
	}

	targetURLProxy = httputil.NewSingleHostReverseProxy(targetURL)
	targetURLProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Println("Failed to reach target service:", err)
		w.WriteHeader(http.StatusBadGateway)
		err := json.NewEncoder(w).Encode(gin.H{
			"error": "something went wrong",
		})
		if err != nil {
			log.Println("Failed to send error response:", err)
		}
	}

	ginTargetURLProxy := gin.WrapH(targetURLProxy)
	r := gin.New()
	r.NoRoute(authenticateHandler)
	r.Use(gin.Logger(), errorMiddleware)
	for _, prefix := range strings.Split(ignoreAuthPaths, ",") {
		if prefix != "" {
			if prefix[len(prefix)-1] == '/' {
				if len(prefix) > 1 {
					r.Any(prefix[:len(prefix)-1], ginTargetURLProxy)
				}
				r.Any(prefix+"*path", ginTargetURLProxy)
			} else {
				r.Any(prefix, ginTargetURLProxy)
			}
		}
	}

	log.Println("Listening on port", port)
	server := http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: r,
	}
	go func() { err = server.ListenAndServe() }()
	<-ctx.Done()
	log.Println("Stopping:", err)
	server.Shutdown(ctx)
	return 0
}

func main() {
	os.Exit(run())
}
