package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr, DatabaseURL, Origin                                            string
	EncryptionKey                                                            []byte
	CookieSecure                                                             bool
	DevelopmentHosts                                                         []string
	Projects, Endpoints, Targets, MonthlyEvents, OrganizationJobs, QueueJobs int
	StorageLimitBytes                                                        int64
	IngestRate, MutationRate, LoginRate                                      int
	StorageReserveBytes                                                      int64
}

func Load() (Config, error) {
	c := Config{HTTPAddr: value("HTTP_ADDR", "127.0.0.1:8080"), DatabaseURL: os.Getenv("DATABASE_URL"), Origin: value("PUBLIC_ORIGIN", "http://localhost:3000"), CookieSecure: os.Getenv("COOKIE_SECURE") != "false"}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return c, fmt.Errorf("HTTP_ADDR must be host:port: %w", err)
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return c, fmt.Errorf("PUBLIC_ORIGIN must be an HTTP(S) origin without a path")
	}
	if !c.CookieSecure && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return c, fmt.Errorf("insecure cookies are allowed only with a localhost PUBLIC_ORIGIN")
	}
	c.EncryptionKey, err = base64.StdEncoding.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil || len(c.EncryptionKey) != 32 {
		return c, fmt.Errorf("ENCRYPTION_KEY must be a base64 encoded 32-byte key")
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if hosts := os.Getenv("DEV_ENDPOINT_HOSTS"); hosts != "" {
		if os.Getenv("APP_ENV") != "development" {
			return c, fmt.Errorf("DEV_ENDPOINT_HOSTS requires APP_ENV=development")
		}
		c.DevelopmentHosts = strings.Split(hosts, ",")
	}
	fields := []struct {
		name     string
		dst      *int
		fallback int
	}{{"RATE_INGEST_PER_MINUTE", &c.IngestRate, 60}, {"RATE_MUTATIONS_PER_MINUTE", &c.MutationRate, 30}, {"RATE_LOGIN_PER_15_MINUTES", &c.LoginRate, 10}, {"LIMIT_PROJECTS", &c.Projects, 5}, {"LIMIT_ENDPOINTS", &c.Endpoints, 10}, {"LIMIT_TARGETS", &c.Targets, 10}, {"LIMIT_MONTHLY_EVENTS", &c.MonthlyEvents, 10000}, {"LIMIT_ORGANIZATION_JOBS", &c.OrganizationJobs, 10000}, {"LIMIT_QUEUE_JOBS", &c.QueueJobs, 100000}}
	for _, f := range fields {
		n, e := strconv.Atoi(value(f.name, strconv.Itoa(f.fallback)))
		if e != nil || n < 1 {
			return c, fmt.Errorf("%s must be positive", f.name)
		}
		*f.dst = n
	}
	if c.Targets > 10 {
		return c, fmt.Errorf("LIMIT_TARGETS cannot exceed the current API schema maximum of 10")
	}
	c.StorageLimitBytes, err = strconv.ParseInt(value("STORAGE_LIMIT_BYTES", "10737418240"), 10, 64)
	if err != nil || c.StorageLimitBytes < 1 {
		return c, fmt.Errorf("STORAGE_LIMIT_BYTES must be positive")
	}
	c.StorageReserveBytes, err = strconv.ParseInt(value("STORAGE_RESERVE_BYTES", "268435456"), 10, 64)
	if err != nil || c.StorageReserveBytes < 0 || c.StorageReserveBytes >= c.StorageLimitBytes {
		return c, fmt.Errorf("STORAGE_RESERVE_BYTES must be nonnegative and smaller than STORAGE_LIMIT_BYTES")
	}
	return c, nil
}
func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
