# RTSPtoWSMP4f

Based on: https://github.com/deepch/RTSPtoWSMP4f

## Run locally

Start video-server: docker run --rm -it -p 8554:8554 tii-video-server

Start video-streamer: go run . 127.0.0.1:8554

Navigate to: http://localhost:8084/test