module github.com/tiiuae/fleet-management/simulation-coordinator

go 1.14

require (
	cloud.google.com/go/pubsub v1.3.1
	github.com/eclipse/paho.mqtt.golang v1.3.5
	github.com/google/uuid v1.1.2
	github.com/gorilla/websocket v1.4.2
	github.com/hashicorp/go-multierror v1.1.1
	github.com/julienschmidt/httprouter v1.3.0
	golang.org/x/oauth2 v0.0.0-20210113205817-d3ed898aa8a3
	google.golang.org/api v0.30.0
	google.golang.org/appengine v1.6.7 // indirect
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v0.21.2
	k8s.io/kubectl v0.21.2
)
