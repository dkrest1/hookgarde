package config

import (
	"encoding/base64"
	"testing"
)

func TestConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PUBLIC_ORIGIN", "http://localhost:3000")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("DEV_ENDPOINT_HOSTS", "")
	c, e := Load()
	if e != nil || c.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal(c, e)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	c, e = Load()
	if e != nil || c.HTTPAddr != "127.0.0.1:9090" {
		t.Fatal(c, e)
	}
	t.Setenv("PUBLIC_ORIGIN", "https://public.example")
	if _, e = Load(); e == nil {
		t.Fatal("insecure public cookies allowed")
	}
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("DEV_ENDPOINT_HOSTS", "receiver")
	t.Setenv("APP_ENV", "production")
	if _, e = Load(); e == nil {
		t.Fatal("production destination bypass allowed")
	}
}
