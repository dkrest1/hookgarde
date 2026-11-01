package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
	"github.com/dkrest1/hookgarde/apps/api/internal/store"
	"github.com/google/uuid"
)

type API struct {
	Store         *store.Store
	Config        config.Config
	Policy        security.Policy
	Log           *slog.Logger
	requests      atomic.Uint64
	failures      atomic.Uint64
	authSlots     chan struct{}
	durationNanos atomic.Uint64
	durationCount atomic.Uint64
}
type operation func(http.ResponseWriter, *http.Request, model.Principal) error

func (a *API) fail(w http.ResponseWriter, err error) {
	var e *model.Error
	if !errors.As(err, &e) {
		a.failures.Add(1)
		a.Log.Error("request failed", "request_id", w.Header().Get("X-Request-ID"))
		e = &model.Error{Status: 503, Code: "service_unavailable", Message: "Service temporarily unavailable"}
	}
	if e.Status == 429 || e.Status == 503 {
		w.Header().Set("Retry-After", "60")
	}
	body := map[string]any{"code": e.Code, "message": e.Message, "request_id": w.Header().Get("X-Request-ID")}
	if e.Code == "quota_exceeded" {
		now := time.Now().UTC()
		body["details"] = map[string]any{"reset_at": time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)}
	}
	writeJSON(w, e.Status, map[string]any{"error": body})
}
func (a *API) route(mux *http.ServeMux, path string, auth string, methods map[string]operation) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		op, ok := methods[r.Method]
		if !ok {
			allowed := []string{}
			for m := range methods {
				allowed = append(allowed, m)
			}
			sort.Strings(allowed)
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			a.fail(w, model.Fail(405, "method_not_allowed", "Unsupported HTTP method"))
			return
		}
		for _, name := range []string{"project_id", "endpoint_id", "key_id", "event_id", "delivery_id", "organization_id"} {
			if v := r.PathValue(name); v != "" {
				if _, e := uuid.Parse(v); e != nil {
					a.fail(w, model.Fail(404, "not_found", "Resource not found"))
					return
				}
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		var p model.Principal
		var e error
		if auth == "ingest" {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == r.Header.Get("Authorization") || token == "" {
				a.fail(w, model.Fail(401, "unauthorized", "Bearer ingestion key required"))
				return
			}
			p, e = a.Store.Bearer(ctx, token, r.PathValue("project_id"))
		} else if auth == "owner" {
			cookie, err := r.Cookie("hookgarde_session")
			if err != nil {
				a.fail(w, model.Fail(401, "unauthorized", "Login required"))
				return
			}
			p, e = a.Store.Session(ctx, cookie.Value)
		}
		if e != nil {
			a.fail(w, e)
			return
		}
		if auth == "owner" && r.Method != "GET" {
			if r.Header.Get("Origin") != a.Config.Origin || !security.EqualHash(r.Header.Get("X-CSRF-Token"), p.CSRFHash) {
				a.fail(w, model.Fail(403, "csrf_failed", "Valid Origin and CSRF token required"))
				return
			}
			e = a.Store.Rate(ctx, p.UserID, "mutation", rate(a.Config.MutationRate, 30), time.Minute)
		} else if auth == "ingest" {
			e = a.Store.Rate(ctx, p.ProjectID, "ingest", rate(a.Config.IngestRate, 60), time.Minute)
		}
		if e != nil {
			a.fail(w, e)
			return
		}
		if e = op(w, r, p); e != nil {
			a.fail(w, e)
		}
	})
}
func (a *API) Handler() http.Handler {
	if a.Log == nil {
		a.Log = slog.Default()
	}
	a.authSlots = make(chan struct{}, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", HealthHandler)
	a.route(mux, "/health/ready", "", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, _ model.Principal) error {
		if e := a.Store.Ready(r.Context()); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return nil
	}})
	a.route(mux, "/metrics", "", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, _ model.Principal) error {
		data, e := a.Store.Metrics(r.Context())
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, e = fmt.Fprintf(w, "%s# TYPE hookgarde_http_requests_total counter\nhookgarde_http_requests_total %d\n# TYPE hookgarde_database_errors_total counter\nhookgarde_database_errors_total %d\n", data, a.requests.Load(), a.failures.Load())
		if e == nil {
			_, e = fmt.Fprintf(w, "# TYPE hookgarde_http_duration_seconds summary\nhookgarde_http_duration_seconds_sum %f\nhookgarde_http_duration_seconds_count %d\n", float64(a.durationNanos.Load())/float64(time.Second), a.durationCount.Load())
		}
		return e
	}})
	a.route(mux, "/v1/auth/login", "", map[string]operation{"POST": a.login})
	a.route(mux, "/v1/auth/logout", "owner", map[string]operation{"POST": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		if e := a.Store.Logout(r.Context(), p); e != nil {
			return e
		}
		a.cookie(w, "", -1)
		w.WriteHeader(204)
		return nil
	}})
	a.route(mux, "/v1/auth/csrf", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, _ model.Principal) error {
		cookie, _ := r.Cookie("hookgarde_session")
		writeJSON(w, 200, map[string]string{"csrf_token": security.CSRF(string(a.Config.EncryptionKey), cookie.Value)})
		return nil
	}})
	a.route(mux, "/v1/me", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		v, e := a.me(r.Context(), p)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}})
	a.route(mux, "/v1/organizations", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		v, e := a.organization(r.Context(), p)
		if e != nil {
			return e
		}
		writeJSON(w, 200, model.List{Data: []any{v}})
		return nil
	}})
	a.route(mux, "/v1/organizations/{organization_id}", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		if r.PathValue("organization_id") != p.OrganizationID {
			return model.Fail(404, "not_found", "Resource not found")
		}
		v, e := a.organization(r.Context(), p)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}})
	a.route(mux, "/v1/organizations/{organization_id}/memberships", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		if r.PathValue("organization_id") != p.OrganizationID {
			return model.Fail(404, "not_found", "Resource not found")
		}
		var created time.Time
		if e := a.Store.DB.QueryRowContext(r.Context(), "SELECT created_at FROM organization_memberships WHERE organization_id=$1 AND user_id=$2", p.OrganizationID, p.UserID).Scan(&created); e != nil {
			return e
		}
		writeJSON(w, 200, model.List{Data: []any{map[string]any{"user_id": p.UserID, "role": "owner", "created_at": created}}})
		return nil
	}})
	a.route(mux, "/v1/projects", "owner", map[string]operation{"GET": a.projects, "POST": a.createProject})
	a.route(mux, "/v1/projects/{project_id}", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		v, e := a.Store.Project(r.Context(), p.OrganizationID, r.PathValue("project_id"))
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}, "PATCH": a.updateProject})
	a.route(mux, "/v1/projects/{project_id}/endpoints", "owner", map[string]operation{"GET": a.endpoints, "POST": a.createEndpoint})
	a.route(mux, "/v1/projects/{project_id}/endpoints/{endpoint_id}", "owner", map[string]operation{"PATCH": a.updateEndpoint})
	a.route(mux, "/v1/projects/{project_id}/endpoints/{endpoint_id}/rotate-secret", "owner", map[string]operation{"POST": a.rotate})
	a.route(mux, "/v1/projects/{project_id}/keys", "owner", map[string]operation{"GET": a.keys, "POST": a.createKey})
	a.route(mux, "/v1/projects/{project_id}/keys/{key_id}", "owner", map[string]operation{"DELETE": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		if e := a.Store.RevokeKey(r.Context(), p, r.PathValue("project_id"), r.PathValue("key_id")); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	}})
	// One path supports different authentication contracts for reading/publishing.
	mux.HandleFunc("/v1/projects/{project_id}/events", func(w http.ResponseWriter, r *http.Request) {
		inner := http.NewServeMux()
		auth := "owner"
		if r.Method == "POST" {
			auth = "ingest"
		}
		a.route(inner, "/v1/projects/{project_id}/events", auth, map[string]operation{"GET": a.events, "POST": a.accept})
		inner.ServeHTTP(w, r)
	})
	a.route(mux, "/v1/projects/{project_id}/events/{event_id}", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		v, e := a.Store.Event(r.Context(), p.OrganizationID, r.PathValue("project_id"), r.PathValue("event_id"))
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}})
	a.route(mux, "/v1/projects/{project_id}/deliveries/{delivery_id}/attempts", "owner", map[string]operation{"GET": a.attempts})
	a.route(mux, "/v1/projects/{project_id}/deliveries/{delivery_id}/replay", "owner", map[string]operation{"POST": a.replay})
	a.route(mux, "/v1/usage", "owner", map[string]operation{"GET": func(w http.ResponseWriter, r *http.Request, p model.Principal) error {
		v, e := a.Store.Usage(r.Context(), p.OrganizationID)
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.fail(w, model.Fail(404, "not_found", "Route not found"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		started := time.Now()
		defer func() { a.durationNanos.Add(uint64(time.Since(started))); a.durationCount.Add(1) }()
		w.Header().Set("X-Request-ID", store.ID())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recover() != nil {
				a.Log.Error("request panic", "request_id", w.Header().Get("X-Request-ID"))
				a.fail(w, model.Fail(500, "internal_error", "Internal server error"))
			}
		}()
		mux.ServeHTTP(w, r)
	})
}
func (a *API) cookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "hookgarde_session", Value: token, Path: "/", HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (a *API) login(w http.ResponseWriter, r *http.Request, _ model.Principal) error {
	if r.Header.Get("Origin") != a.Config.Origin {
		return model.Fail(403, "origin_forbidden", "Origin is not allowed")
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if e := a.Store.Rate(r.Context(), host, "login_ip", rate(a.Config.LoginRate, 10), 15*time.Minute); e != nil {
		return e
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	if len(input.Email) > 254 || utf8.RuneCountInString(input.Password) < 12 || utf8.RuneCountInString(input.Password) > 128 {
		return model.Fail(401, "invalid_credentials", "Invalid email or password")
	}
	if e := a.Store.Rate(r.Context(), strings.ToLower(input.Email), "login_account", rate(a.Config.LoginRate, 10), 15*time.Minute); e != nil {
		return e
	}
	select {
	case a.authSlots <- struct{}{}:
		defer func() { <-a.authSlots }()
	default:
		return model.Fail(429, "login_busy", "Login capacity busy; retry shortly")
	}
	p, token, e := a.Store.Login(r.Context(), input.Email, input.Password)
	if e != nil {
		return e
	}
	v, e := a.me(r.Context(), p)
	if e != nil {
		return e
	}
	a.cookie(w, token, 86400)
	writeJSON(w, 200, v)
	return nil
}
func (a *API) limits() map[string]int {
	return map[string]int{"projects": a.Config.Projects, "active_endpoints": a.Config.Endpoints, "targets_per_event": a.Config.Targets, "events_per_month": a.Config.MonthlyEvents}
}
func (a *API) organization(ctx context.Context, p model.Principal) (map[string]any, error) {
	var created time.Time
	e := a.Store.DB.QueryRowContext(ctx, "SELECT created_at FROM organizations WHERE id=$1", p.OrganizationID).Scan(&created)
	return map[string]any{"id": p.OrganizationID, "name": p.OrganizationName, "created_at": created, "limits": a.limits()}, e
}
func (a *API) me(ctx context.Context, p model.Principal) (map[string]any, error) {
	org, e := a.organization(ctx, p)
	return map[string]any{"user": map[string]string{"id": p.UserID, "email": p.Email}, "organization": org, "role": "owner"}, e
}
func page(r *http.Request) (store.Page, error) {
	return store.ParsePage(r.URL.Query().Get("limit"), r.URL.Query().Get("cursor"))
}
func validName(s string, n int) bool {
	return utf8.RuneCountInString(strings.TrimSpace(s)) > 0 && utf8.RuneCountInString(s) <= n
}
func invalid(message string) error { return model.Fail(422, "validation_failed", message) }
func idem(r *http.Request) (string, error) {
	s := r.Header.Get("Idempotency-Key")
	if len(s) < 1 || len(s) > 128 {
		return "", invalid("Idempotency-Key must contain 1-128 printable ASCII bytes")
	}
	for _, b := range []byte(s) {
		if b < 32 || b > 126 {
			return "", invalid("Idempotency-Key must contain printable ASCII bytes")
		}
	}
	return s, nil
}

func rate(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}
