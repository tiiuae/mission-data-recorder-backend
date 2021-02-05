# web-backend container

## Building and running container

Build and tag container
```
docker build -t tii-web-backend .
```

Run container in docker
```
docker run --rm -it -p 8083:8083 tii-web-backend <mqtt-broker-address>
```

