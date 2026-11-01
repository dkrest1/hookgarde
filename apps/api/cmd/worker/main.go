package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/delivery"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
	"github.com/dkrest1/hookgarde/apps/api/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if e := run(log); e != nil {
		log.Error("worker stopped", "error", e.Error())
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
	return (delivery.Worker{Store: db, EncryptionKey: c.EncryptionKey, Policy: security.Policy{DevelopmentHosts: c.DevelopmentHosts}, Log: log}).Run(ctx)
}
