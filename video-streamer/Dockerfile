FROM golang:latest as builder
RUN mkdir /video-streamer
WORKDIR /video-streamer/
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY *.go ./
RUN GOOS=linux GOARCH=amd64 go build -o video-streamer .

FROM golang:latest
COPY --from=builder /video-streamer/video-streamer /bin/video-streamer
COPY web/ /bin/web/
WORKDIR /bin
EXPOSE 8084
ENTRYPOINT ["/bin/video-streamer"]
