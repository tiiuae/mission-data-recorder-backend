FROM golang:1.16 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -o firebase-auth-sidecar

FROM scratch

WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /build/firebase-auth-sidecar ./
EXPOSE 9000
ENTRYPOINT [ "/app/firebase-auth-sidecar" ]
