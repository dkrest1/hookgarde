package httpapi

import "net/http"

// HealthHandler checks HTTP liveness only; it must not query PostgreSQL.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, 405, map[string]any{"error": map[string]any{"code": "method_not_allowed", "message": "Only GET is allowed", "request_id": w.Header().Get("X-Request-ID")}})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
