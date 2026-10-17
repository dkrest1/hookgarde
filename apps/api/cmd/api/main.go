package main

import (
	"log"
	"net/http"
	"os"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"status":"ok"}`))

		if err != nil {
			log.Printf("failed to write response: %v", err)
		}
	}

func main() {

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health/live", healthHandler)


	addr := os.Getenv("HTTP_ADDR")

	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	log.Printf("HTTP server listening on localhost:%v 🚀", addr)

	err := http.ListenAndServe(addr, mux)

	if err != nil {
		log.Fatal(err)
	}
}
