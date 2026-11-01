package httpapi

import (
	"bytes"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/google/uuid"
)

func (a *API) projects(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	pg, e := page(r)
	if e != nil {
		return e
	}
	v, e := a.Store.Projects(r.Context(), p.OrganizationID, pg)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) createProject(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	var input struct {
		Name string `json:"name"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	if !validName(input.Name, 120) {
		return invalid("Name must contain 1-120 characters")
	}
	v, e := a.Store.CreateProject(r.Context(), p, input.Name)
	if e != nil {
		return e
	}
	writeJSON(w, 201, v)
	return nil
}
func (a *API) updateProject(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	var input struct {
		Name     *string `json:"name"`
		Archived *bool   `json:"archived"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	if input.Name == nil && input.Archived == nil {
		return invalid("At least one field is required")
	}
	if input.Name != nil && !validName(*input.Name, 120) {
		return invalid("Name must contain 1-120 characters")
	}
	v, e := a.Store.UpdateProject(r.Context(), p, r.PathValue("project_id"), input.Name, input.Archived)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) endpoints(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	pg, e := page(r)
	if e != nil {
		return e
	}
	v, e := a.Store.Endpoints(r.Context(), p.OrganizationID, r.PathValue("project_id"), pg)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) createEndpoint(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	var input struct {
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return invalid("Description cannot exceed 500 characters")
	}
	if e := a.Policy.Validate(r.Context(), input.URL); e != nil {
		return invalid("Destination must satisfy the configured HTTPS and public-address policy")
	}
	v, e := a.Store.CreateEndpoint(r.Context(), p, r.PathValue("project_id"), input.URL, input.Description)
	if e != nil {
		return e
	}
	secret := v.SigningSecret
	v.SigningSecret = ""
	writeJSON(w, 201, map[string]any{"endpoint": v, "signing_key": map[string]any{"key_version": 1, "secret": secret, "activates_at": v.CreatedAt, "previous_retires_at": nil}})
	return nil
}
func (a *API) updateEndpoint(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	var input struct {
		URL         *string `json:"url"`
		Description *string `json:"description"`
		Paused      *bool   `json:"paused"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	if input.URL == nil && input.Description == nil && input.Paused == nil {
		return invalid("At least one field is required")
	}
	if input.Description != nil && utf8.RuneCountInString(*input.Description) > 500 {
		return invalid("Description cannot exceed 500 characters")
	}
	if input.URL != nil {
		if e := a.Policy.Validate(r.Context(), *input.URL); e != nil {
			return invalid("Destination must satisfy the configured HTTPS and public-address policy")
		}
	}
	v, e := a.Store.UpdateEndpoint(r.Context(), p, r.PathValue("project_id"), r.PathValue("endpoint_id"), input.URL, input.Description, input.Paused)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) rotate(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	var input struct {
		Emergency bool `json:"emergency"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	v, e := a.Store.Rotate(r.Context(), p, r.PathValue("project_id"), r.PathValue("endpoint_id"), input.Emergency)
	if e != nil {
		return e
	}
	writeJSON(w, 201, v)
	return nil
}
func (a *API) keys(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	pg, e := page(r)
	if e != nil {
		return e
	}
	v, e := a.Store.Keys(r.Context(), p.OrganizationID, r.PathValue("project_id"), pg)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) createKey(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	var input struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if _, e := decode(w, r, &input); e != nil {
		return e
	}
	if !validName(input.Name, 120) {
		return invalid("Name must contain 1-120 characters")
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return invalid("Expiry must be in the future")
	}
	v, e := a.Store.CreateKey(r.Context(), p, r.PathValue("project_id"), input.Name, input.ExpiresAt)
	if e != nil {
		return e
	}
	token := v.Token
	v.Token = ""
	writeJSON(w, 201, map[string]any{"key": v, "token": token})
	return nil
}
func (a *API) accept(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	key, e := idem(r)
	if e != nil {
		return e
	}
	var input model.EventInput
	raw, e := decode(w, r, &input)
	if e != nil {
		return e
	}
	if !validName(input.Type, 128) || len(input.EndpointIDs) < 1 || len(input.EndpointIDs) > a.Config.Targets {
		return invalid("Provide an event type and 1-10 explicit endpoints")
	}
	seen := map[string]bool{}
	for _, id := range input.EndpointIDs {
		parsed, e := uuid.Parse(id)
		if e != nil || parsed.String() != id || seen[id] {
			return invalid("Endpoint IDs must be unique canonical UUIDs")
		}
		seen[id] = true
	}
	data := bytes.TrimSpace(input.Data)
	if len(data) == 0 || data[0] != '{' {
		return invalid("Event data must be a JSON object")
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	v, e := a.Store.Accept(r.Context(), p, key, raw, input)
	if e != nil {
		return e
	}
	writeJSON(w, 202, v)
	return nil
}
func (a *API) events(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	pg, e := page(r)
	if e != nil {
		return e
	}
	q := r.URL.Query()
	from, to := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if q.Get("from") != "" {
		from, e = time.Parse(time.RFC3339, q.Get("from"))
		if e != nil {
			return model.Fail(400, "invalid_filter", "Invalid from timestamp")
		}
	}
	if q.Get("to") != "" {
		to, e = time.Parse(time.RFC3339, q.Get("to"))
		if e != nil {
			return model.Fail(400, "invalid_filter", "Invalid to timestamp")
		}
	}
	if from.After(to) {
		return model.Fail(400, "invalid_filter", "from must not be after to")
	}
	status := q.Get("status")
	if status != "" && status != "pending" && status != "completed" && status != "completed_with_failures" {
		return model.Fail(400, "invalid_filter", "Unknown event status")
	}
	if len(q.Get("type")) > 128 {
		return model.Fail(400, "invalid_filter", "Event type filter is too long")
	}
	v, e := a.Store.Events(r.Context(), p.OrganizationID, r.PathValue("project_id"), pg, q.Get("type"), status, from, to)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) attempts(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	pg, e := page(r)
	if e != nil {
		return e
	}
	v, e := a.Store.Attempts(r.Context(), p.OrganizationID, r.PathValue("project_id"), r.PathValue("delivery_id"), pg)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *API) replay(w http.ResponseWriter, r *http.Request, p model.Principal) error {
	key, e := idem(r)
	if e != nil {
		return e
	}
	var input struct{}
	raw, e := decode(w, r, &input)
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return invalid("Replay requires an empty object")
	}
	v, e := a.Store.Replay(r.Context(), p, r.PathValue("project_id"), r.PathValue("delivery_id"), key, raw)
	if e != nil {
		return e
	}
	writeJSON(w, 202, v)
	return nil
}
