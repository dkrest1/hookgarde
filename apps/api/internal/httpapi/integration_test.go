package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/delivery"
	"github.com/dkrest1/hookgarde/apps/api/internal/httpapi"
	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
	"github.com/dkrest1/hookgarde/apps/api/internal/store"
)

func TestOwnerToSignedDeliveryJourney(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	c := config.Config{DatabaseURL: dsn, Origin: "http://localhost:3000", EncryptionKey: make([]byte, 32), Projects: 5, Endpoints: 10, Targets: 10, MonthlyEvents: 10000, OrganizationJobs: 10000, QueueJobs: 100000, StorageLimitBytes: 1 << 40}
	db, e := store.Open(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	email := store.ID() + "@example.com"
	if e = db.Provision(ctx, email, "correct-horse-battery-staple", "HTTP test"); e != nil {
		t.Fatal(e)
	}
	api := (&httpapi.API{Store: db, Config: c, Policy: security.Policy{DevelopmentHosts: []string{"127.0.0.1"}}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler()
	var session *http.Cookie
	csrf := ""
	call := func(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:12345"
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("Origin", c.Origin)
		if session != nil {
			r.AddCookie(session)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	read := func(w *httptest.ResponseRecorder, status int, schemas ...string) map[string]any {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d expected %d: %s", w.Code, status, w.Body.String())
		}
		var v map[string]any
		if status != 204 {
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
				t.Fatal(e)
			}
		}
		if len(schemas) > 0 {
			schema(t, schemas[0], v)
		}
		return v
	}
	read(call("GET", "/v1/projects", nil, nil), 401)
	w := call("POST", "/v1/auth/login", map[string]string{"email": email, "password": "correct-horse-battery-staple"}, nil)
	read(w, 200, "Me")
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("session cookie contract")
	}
	session = cookies[0]
	csrf = read(call("GET", "/v1/auth/csrf", nil, nil), 200, "CsrfToken")["csrf_token"].(string)
	read(call("POST", "/v1/projects", map[string]string{"name": "bad origin"}, map[string]string{"Origin": "https://evil.example"}), 403)
	read(call("POST", "/v1/projects", map[string]string{"name": "bad csrf"}, map[string]string{"X-CSRF-Token": "wrong"}), 403)
	project := read(call("POST", "/v1/projects", map[string]string{"name": "Orders"}, nil), 201, "Project")["id"].(string)
	received := make(chan *http.Request, 1)
	bodies := make(chan []byte, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- r.Clone(ctx)
		bodies <- body
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	ep := read(call("POST", "/v1/projects/"+project+"/endpoints", map[string]string{"url": receiver.URL, "description": "receiver"}, nil), 201, "EndpointCreated")
	endpoint := ep["endpoint"].(map[string]any)["id"].(string)
	secret := ep["signing_key"].(map[string]any)["secret"].(string)
	key := read(call("POST", "/v1/projects/"+project+"/keys", map[string]string{"name": "publisher"}, nil), 201, "KeyCreated")["token"].(string)
	input := map[string]any{"type": "order.paid", "endpoint_ids": []string{endpoint}, "data": map[string]string{"id": "123"}}
	headers := map[string]string{"Authorization": "Bearer " + key, "Idempotency-Key": "journey"}
	receipt := read(call("POST", "/v1/projects/"+project+"/events", input, headers), 202, "Acceptance")
	read(call("GET", "/v1/projects/"+project+"/events", nil, map[string]string{"Cookie": "", "Authorization": "Bearer " + key}), 401)
	again := read(call("POST", "/v1/projects/"+project+"/events", input, headers), 202, "Acceptance")
	if again["event_id"] != receipt["event_id"] || again["idempotency_replayed"] != true {
		t.Fatal("dedupe response")
	}
	target := receipt["delivery_ids"].([]any)[0].(string)
	var claim *model.Claim
	for i := 0; i < 100; i++ {
		claim, e = db.Claim(ctx, []string{})
		if e != nil || claim == nil {
			t.Fatal(e)
		}
		if claim.DeliveryID == target {
			break
		}
		if e = db.Complete(ctx, *claim, model.Outcome{Success: true, Status: 204}); e != nil {
			t.Fatal(e)
		}
	}
	policy := security.Policy{DevelopmentHosts: []string{"127.0.0.1"}}
	outcome := delivery.Send(ctx, *claim, c.EncryptionKey, policy, policy.Client())
	if !outcome.Success {
		t.Fatal(outcome)
	}
	if e = db.Complete(ctx, *claim, outcome); e != nil {
		t.Fatal(e)
	}
	r := <-received
	body := <-bodies
	signature := r.Header.Get("Webhook-Signature")
	parts := strings.Split(signature, ",")
	var ts int64
	if _, e := fmt.Sscan(strings.TrimPrefix(parts[0], "t="), &ts); e != nil {
		t.Fatal(e)
	}
	if got, e := security.Signature(secret, ts, 1, body); e != nil || got != signature {
		t.Fatal("invalid delivered signature")
	}
	if !bytes.Equal(body, claim.Body) {
		t.Fatal("delivered envelope changed")
	}
	eventID := receipt["event_id"].(string)
	detail := read(call("GET", "/v1/projects/"+project+"/events/"+eventID, nil, nil), 200, "EventDetail")
	if detail["event"].(map[string]any)["status"] != "completed" {
		t.Fatal(detail)
	}
	read(call("GET", "/v1/projects/"+project+"/deliveries/"+target+"/attempts", nil, nil), 200, "AttemptList")
	read(call("GET", "/v1/usage", nil, nil), 200, "Usage")
	read(call("POST", "/v1/auth/logout", nil, nil), 204)
	read(call("GET", "/v1/me", nil, nil), 401)
}
