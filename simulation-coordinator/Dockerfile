FROM golang:latest as builder
WORKDIR /simulation-coordinator
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o simulation-coordinator .

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /simulation-coordinator/simulation-coordinator /bin/simulation-coordinator
EXPOSE 8087
ENTRYPOINT ["/bin/simulation-coordinator"]
