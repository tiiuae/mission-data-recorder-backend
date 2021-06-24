FROM golang:latest as builder
RUN mkdir /device-management
WORKDIR /device-management/
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY *.go ./
RUN GOOS=linux GOARCH=amd64 go build -o device-management .


FROM golang:latest
COPY --from=builder /device-management/device-management /bin/device-management
EXPOSE 8083
ENTRYPOINT ["/bin/device-management"]
