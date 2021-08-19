package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var Config *ConfigST

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Println("usage: video-streamer <video-server> [base-url]")
		return
	}

	videoServerAddress := os.Args[1]
	baseURL := "/"
	if len(os.Args) > 2 {
		baseURL = os.Args[2]
	}

	Config = loadConfig(videoServerAddress, baseURL)

	go serveHTTP()
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Println(sig)
		done <- true
	}()
	log.Println("Server Start Awaiting Signal")
	<-done
	log.Println("Exiting")
}
