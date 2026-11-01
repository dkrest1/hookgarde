package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/httpapi"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
	"github.com/dkrest1/hookgarde/apps/api/internal/server"
	"github.com/dkrest1/hookgarde/apps/api/internal/store"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "probe" {
		client := &http.Client{Timeout: 3 * time.Second}
		r, e := client.Get("http://127.0.0.1:8080/health/ready")
		if e != nil {
			fmt.Fprintln(os.Stderr, "API is not ready")
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if e := run(log); e != nil {
		log.Error("API stopped", "error", e.Error())
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	c, e := config.Load()
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, e := store.Open(startup, c)
	if e != nil {
		return e
	}
	defer db.DB.Close()
	if e = db.Ready(startup); e != nil {
		return e
	}
	api := &httpapi.API{Store: db, Config: c, Policy: security.Policy{DevelopmentHosts: c.DevelopmentHosts}, Log: log}
	log.Info("API starting", "address", c.HTTPAddr)
	return server.Run(ctx, c.HTTPAddr, api.Handler())
}
