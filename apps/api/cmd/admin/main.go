// The operator CLI provisions owners without exposing public registration.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/store"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: admin migrate|provision|maintain|reconcile")
	}
	command := os.Args[1]
	if command == "receiver" {
		return demoReceiver()
	}
	if command != "migrate" && command != "provision" && command != "maintain" && command != "reconcile" {
		return fmt.Errorf("unknown admin command")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	email := flags.String("email", "", "owner email")
	org := flags.String("organization", "", "organization name")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	c, e := config.Load()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, e := store.Open(ctx, c)
	if e != nil {
		return e
	}
	defer db.DB.Close()
	switch command {
	case "migrate":
		e = db.Migrate(ctx)
	case "provision":
		password := os.Getenv("OWNER_PASSWORD")
		if password == "" {
			return fmt.Errorf("OWNER_PASSWORD environment variable is required")
		}
		e = db.Provision(ctx, *email, password, *org)
	case "maintain":
		e = db.Maintain(ctx)
	case "reconcile":
		e = db.Reconcile(ctx)
	}
	if e != nil {
		return e
	}
	fmt.Println(command + " completed")
	return nil
}

// demoReceiver never logs bodies, signatures or secrets. It can deliberately
// return 503 for recovery exercises via RECEIVER_STATUS=503.
func demoReceiver() error {
	status := 204
	if os.Getenv("RECEIVER_STATUS") == "503" {
		status = 503
	}
	return http.ListenAndServe(":8090", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(status)
	}))
}
