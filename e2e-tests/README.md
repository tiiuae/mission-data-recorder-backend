## Run tests locally

### Prerequisites
* tmux, nodejs v12 etc. installed
* e2e-drone-emulator: yarn install (ros2 required)
* e2e-tests: yarn install

```bash
# Start local MQTT server
$ docker run --rm -it -p 8883:8883 tii-mqtt-server

# Run test
$ ./tmux-run-test.sh src/01-telemetry.ts
```

## Run tests in minikube

```bash
# Build test container to minikube environment
$ docker build -t tii-e2e-tests:latest .

# Setup simulation
$ dronsole sim create s1
$ dronsole sim drones add s1 d1 -x 0
$ dronsole sim drones add s1 d2 -x 1

# Start test container
kubectl run -i -n s1 --tty e2e-tests --image=tii-e2e-tests:latest --image-pull-policy=Never --restart=Never -- /bin/bash

# Run test
root@e2e-tests:/e2e-test# ./run-test-kube.sh 03
```
