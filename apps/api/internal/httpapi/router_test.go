package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
)

func TestHealthRouting(t *testing.T) {
	a := &API{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := a.Handler()
	for _, tc := range []struct {
		method, path string
		status       int
		allow        string
	}{{"GET", "/health/live", 200, ""}, {"POST", "/health/live", 405, "GET"}, {"HEAD", "/health/live", 405, "GET"}, {"PUT", "/health/live", 405, "GET"}, {"PATCH", "/health/live", 405, "GET"}, {"DELETE", "/health/live", 405, "GET"}, {"OPTIONS", "/health/live", 405, "GET"}, {"GET", "/health/live/", 404, ""}, {"GET", "/health/live/extra", 404, ""}, {"GET", "/missing", 404, ""}, {"POST", "/missing", 404, ""}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || w.Header().Get("Allow") != tc.allow {
				t.Fatalf("got %d Allow=%q", w.Code, w.Header().Get("Allow"))
			}
			if tc.status == 200 {
				var body map[string]string
				if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || body["status"] != "ok" || w.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("invalid health response: %s", w.Body.String())
				}
			}
		})
	}
}
func TestStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{{"valid", `{"name":"test"}`, true}, {"duplicate", `{"name":"a","name":"b"}`, false}, {"nested duplicate", `{"name":{"x":1,"x":2}}`, false}, {"unknown", `{"other":1}`, false}, {"wrong case", `{"Name":"a"}`, false}, {"null field", `{"name":null}`, false}, {"trailing", `{"name":"a"}{}`, false}, {"array", `[]`, false}, {"null", `null`, false}, {"oversized", `{"name":"` + strings.Repeat("a", MaxBody) + `"}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			var input struct {
				Name string `json:"name"`
			}
			_, e := decode(httptest.NewRecorder(), r, &input)
			if (e == nil) != tc.ok {
				t.Fatalf("error=%v", e)
			}
		})
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	_, e := decode(httptest.NewRecorder(), r, &struct{}{})
	if e.(*model.Error).Status != 415 {
		t.Fatal(e)
	}
}
