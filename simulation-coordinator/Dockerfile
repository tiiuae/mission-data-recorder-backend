FROM golang:latest as builder
RUN mkdir /simulation-coordinator
WORKDIR /simulation-coordinator/
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY *.go ./
RUN GOOS=linux GOARCH=amd64 go build -o simulation-coordinator .


FROM golang:latest
COPY --from=builder /simulation-coordinator/simulation-coordinator /bin/simulation-coordinator
EXPOSE 8087
ENTRYPOINT ["/bin/simulation-coordinator"]
