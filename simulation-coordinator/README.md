# simulation-coordinator container

Enables multi-tenancy on the dronsole SaaS K8s cluster.
Isolates the platform users from the complexities of K8s.
Provisions new namespaces for authorized users and deploys simulation entities in it.

## Building and running container

Build and tag container
```
docker build -t ghcr.io/tiiuae/tii-simulation-coordinator .
```

Run container in docker
```
docker run --rm -it -p 8087:8087 ghcr.io/tiiuae/tii-simulation-coordinator
```

