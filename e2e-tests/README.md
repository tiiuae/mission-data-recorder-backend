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
$ docker build -t eu.gcr.io/auto-fleet-mgnt/tii-e2e-tests:latest .

# Start test container
$ kubectl run -i -n dronsole --tty e2e-tests --image=eu.gcr.io/auto-fleet-mgnt/tii-e2e-tests:latest --image-pull-policy=Never --restart=Never --rm -- /bin/bash

# Run test
root@e2e-tests:/e2e-test# ./run-test-kube.sh 03

# Start viewer
$ dronsole sim viewer e2e
```
