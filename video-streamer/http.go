package main

import (
	"log"
	"net/http"
	"time"

	"github.com/deepch/vdk/av"
	"github.com/deepch/vdk/format/mp4f"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
)

func serveHTTP() {
	router := gin.Default()
	gin.SetMode(gin.DebugMode)
	router.LoadHTMLGlob("web/templates/*")
	router.GET("/test", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.tmpl", gin.H{
			"port":    Config.Server.HTTPPort,
			"suuid":   "test-stream-1",
			"baseURL": Config.Server.BaseURL,
		})
	})
	router.GET("/drones/:suuid", droneStreamHandler)
	router.GET("/test/:suuid", droneStreamHandler)
	router.GET("/ws/:suuid", func(c *gin.Context) {
		handler := websocket.Handler(ws)
		handler.ServeHTTP(c.Writer, c.Request)
	})
	router.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	router.StaticFS("/static", http.Dir("web/static"))
	err := router.Run(Config.Server.HTTPPort)
	if err != nil {
		log.Fatalln(err)
	}
}

func droneStreamHandler(c *gin.Context) {
	c.HTML(http.StatusOK, "index.tmpl", gin.H{
		"port":    Config.Server.HTTPPort,
		"suuid":   c.Param("suuid"),
		"baseURL": Config.Server.BaseURL,
	})
}

func ws(ws *websocket.Conn) {
	defer ws.Close()
	suuid := ws.Request().FormValue("suuid")
	log.Println("Request", suuid)
	if !Config.ext(suuid) {
		log.Println("Stream Not Found")
		return
	}
	Config.RunIFNotRun(suuid)
	ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	cuuid, ch := Config.clAd(suuid)
	defer Config.clDe(suuid, cuuid)
	codecs := Config.coGe(suuid)
	if codecs == nil {
		log.Println("Codecs Error")
		return
	}
	for i, codec := range codecs {
		if codec.Type().IsAudio() && codec.Type() != av.AAC {
			log.Println("Track", i, "Audio Codec Work Only AAC")
		}
	}
	muxer := mp4f.NewMuxer(nil)
	err := muxer.WriteHeader(codecs)
	if err != nil {
		log.Println("muxer.WriteHeader", err)
		return
	}
	meta, init := muxer.GetInit(codecs)
	err = websocket.Message.Send(ws, append([]byte{9}, meta...))
	if err != nil {
		log.Println("websocket.Message.Send", err)
		return
	}
	err = websocket.Message.Send(ws, init)
	if err != nil {
		return
	}
	var start bool
	go func() {
		for {
			var message string
			err := websocket.Message.Receive(ws, &message)
			if err != nil {
				ws.Close()
				return
			}
		}
	}()
	noVideoWarning := time.NewTimer(10 * time.Second)
	noVideo := time.NewTimer(30 * time.Second)
	var timeLine = make(map[int8]time.Duration)
	for {
		select {
		case <-noVideoWarning.C:
			log.Println("noVideo 10 sec warning")
		case <-noVideo.C:
			log.Println("noVideo 30 sec -> closing stream")
			return
		case pck := <-ch:
			if pck.IsKeyFrame {
				noVideoWarning.Reset(10 * time.Second)
				noVideo.Reset(30 * time.Second)
				start = true
			}
			if !start {
				continue
			}
			timeLine[pck.Idx] += pck.Duration
			pck.Time = timeLine[pck.Idx]
			ready, buf, _ := muxer.WritePacket(pck, false)
			if ready {
				err = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err != nil {
					return
				}
				err := websocket.Message.Send(ws, buf)
				if err != nil {
					return
				}
			}
		}
	}
}
