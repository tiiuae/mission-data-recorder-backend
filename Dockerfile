FROM golang:1.17 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go build -o mission-data-recorder-backend

FROM ubuntu:20.04
WORKDIR /app
RUN apt-get update && apt-get install ca-certificates -y
COPY --from=builder /build/mission-data-recorder-backend .
EXPOSE 9000
ENTRYPOINT [ "/app/mission-data-recorder-backend" ]
CMD [ "-config", "/app/config/config.yaml" ]
