# mission-data-recorder-backend

Stores files sent by mission-data-recorder to cloud storage.

## Building

The application can be built by running

    go build -o mission-data-recorder-backend

A Dockerfile is also available. The image can be built by running

    docker build -t tii-mission-data-recorder-backend .

## Running

The application can be started by running

    ./mission-data-recorder-backend -config <config-file>

where `<config-file>` is a path to a configuration file in YAML format.
The schema is defined in `main.go`.
