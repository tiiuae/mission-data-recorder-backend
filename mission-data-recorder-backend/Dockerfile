FROM golang:1.16.2 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -a -tags netgo -ldflags '-w' -o mission-data-recorder-backend

FROM scratch
WORKDIR /app
COPY --from=builder /build/mission-data-recorder-backend .
EXPOSE 9000
ENTRYPOINT [ "/app/mission-data-recorder-backend" ]
CMD [ "-config", "/app/config/config.yaml" ]
