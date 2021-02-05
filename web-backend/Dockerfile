FROM golang:latest as builder
RUN mkdir /web-backend
WORKDIR /web-backend/
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY *.go ./
RUN GOOS=linux GOARCH=amd64 go build -o web-backend .


FROM golang:latest
COPY --from=builder /web-backend/web-backend /bin/web-backend
EXPOSE 8083
ENTRYPOINT ["/bin/web-backend"]
