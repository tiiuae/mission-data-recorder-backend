package main

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
)

type jsonObj map[string]interface{}

func writeJSON(rw http.ResponseWriter, val interface{}) {
	if err := json.NewEncoder(rw).Encode(val); err != nil {
		log.Println("failed to write response:", err)
	}
}

func writeErrMsg(rw http.ResponseWriter, code int, msg string) {
	rw.WriteHeader(code)
	writeJSON(rw, jsonObj{"error": msg})
}

type loggerResponseWriter struct {
	http.ResponseWriter
	code int
}

func newLoggerResponseWriter(rw http.ResponseWriter) *loggerResponseWriter {
	return &loggerResponseWriter{
		ResponseWriter: rw,
		code:           -1,
	}
}

func (rw *loggerResponseWriter) WriteHeader(code int) {
	if rw.code < 0 {
		rw.code = code
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *loggerResponseWriter) Write(data []byte) (int, error) {
	rw.WriteHeader(http.StatusOK)
	return rw.ResponseWriter.Write(data)
}

func requestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		logrw := newLoggerResponseWriter(rw)
		next.ServeHTTP(logrw, r)
		log.Printf("%s %s %d %s", r.Proto, r.Method, logrw.code, r.URL.String())
	})
}

func recoverPanicMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic occurred: %v, stacktrace: %s", r, string(debug.Stack()))
				writeErrMsg(
					wr,
					http.StatusInternalServerError,
					"something went wrong",
				)
			}
		}()
		next.ServeHTTP(wr, r)
	})
}

func notFoundHandler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		writeErrMsg(rw, http.StatusNotFound, "not found")
	})
}

func methodNotAllowedHandler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		writeErrMsg(rw, http.StatusMethodNotAllowed, "method not allowed")
	})
}
