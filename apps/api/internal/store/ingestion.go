package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
)

// Accept commits the immutable envelope, all target jobs, dedupe receipt, quota
// and queue slots together. It never sends a request to an external receiver.
func (s *Store) Accept(ctx context.Context, p model.Principal, key string, raw []byte, input model.EventInput) (model.Acceptance, error) {
	var response model.Acceptance
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, ""); e != nil {
			return e
		}
		var valid bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()))", p.KeyID).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return model.Fail(401, "unauthorized", "Ingestion key was revoked or expired")
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM ingestion_requests WHERE project_id=$1 AND key=$2 AND expires_at<=now()", p.ProjectID, key); e != nil {
			return e
		}
		var hash, receipt []byte
		var purged bool
		e := tx.QueryRowContext(ctx, `SELECT r.request_hash,r.acceptance_response,e.envelope_bytes IS NULL FROM ingestion_requests r JOIN events e ON e.id=r.event_id WHERE r.project_id=$1 AND r.key=$2`, p.ProjectID, key).Scan(&hash, &receipt, &purged)
		if e == nil {
			if !bytes.Equal(hash, security.Hash(string(raw))) {
				return model.Fail(409, "idempotency_conflict", "Key was used with different request bytes")
			}
			if purged {
				return model.Fail(410, "payload_purged", "Event payload has expired")
			}
			if e = json.Unmarshal(receipt, &response); e != nil {
				return e
			}
			response.Replayed = true
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		if e = lock(ctx, tx, p.OrganizationID, p.ProjectID); e != nil {
			return e
		}
		targets := append([]string{}, input.EndpointIDs...)
		sort.Strings(targets)
		versions := map[string]string{}
		for _, id := range targets {
			var version string
			var paused bool
			e = tx.QueryRowContext(ctx, "SELECT current_version_id,paused FROM endpoints WHERE organization_id=$1 AND project_id=$2 AND id=$3 FOR UPDATE", p.OrganizationID, p.ProjectID, id).Scan(&version, &paused)
			if e == sql.ErrNoRows {
				return missing()
			}
			if e != nil {
				return e
			}
			if paused {
				return model.Fail(409, "endpoint_paused", "A selected endpoint is paused")
			}
			versions[id] = version
		}
		if e = reserve(ctx, tx, s, p.OrganizationID, len(targets)); e != nil {
			return e
		}
		var usage int
		e = tx.QueryRowContext(ctx, `INSERT INTO usage_months(organization_id,month_start,accepted_event_count) VALUES($1,date_trunc('month',now() AT TIME ZONE 'UTC')::date,1) ON CONFLICT(organization_id,month_start) DO UPDATE SET accepted_event_count=usage_months.accepted_event_count+1,updated_at=now() WHERE usage_months.accepted_event_count<$2 RETURNING accepted_event_count`, p.OrganizationID, s.Config.MonthlyEvents).Scan(&usage)
		if e == sql.ErrNoRows {
			return model.Fail(429, "quota_exceeded", "Monthly accepted-event quota reached")
		}
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		response = model.Acceptance{EventID: ID(), Status: "accepted", AcceptedAt: now, DeliveryIDs: []string{}}
		body, e := json.Marshal(model.Envelope{ID: response.EventID, Type: input.Type, ProjectID: p.ProjectID, OccurredAt: input.OccurredAt.UTC(), AcceptedAt: now, Data: input.Data})
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO events(id,organization_id,project_id,type,envelope_bytes,payload_size,occurred_at,accepted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, response.EventID, p.OrganizationID, p.ProjectID, input.Type, body, len(body), input.OccurredAt, now); e != nil {
			return e
		}
		for _, endpoint := range input.EndpointIDs {
			id := ID()
			if _, e = tx.ExecContext(ctx, `INSERT INTO deliveries(id,organization_id,project_id,event_id,endpoint_id,endpoint_version_id,generation,state,next_attempt_at,deadline_at) VALUES($1,$2,$3,$4,$5,$6,1,'pending',$7,$8)`, id, p.OrganizationID, p.ProjectID, response.EventID, endpoint, versions[endpoint], now, now.Add(24*time.Hour)); e != nil {
				return e
			}
			response.DeliveryIDs = append(response.DeliveryIDs, id)
		}
		receipt, e = json.Marshal(response)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO ingestion_requests(id,organization_id,project_id,key,request_hash,event_id,acceptance_response) VALUES($1,$2,$3,$4,$5,$6,$7)`, ID(), p.OrganizationID, p.ProjectID, key, security.Hash(string(raw)), response.EventID, string(receipt)); e != nil {
			return e
		}
		return nil
	})
	return response, e
}
func (s *Store) Replay(ctx context.Context, p model.Principal, project, original, key string, raw []byte) (map[string]any, error) {
	var result map[string]any
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, ""); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM replay_requests WHERE organization_id=$1 AND original_delivery_id=$2 AND key=$3 AND expires_at<=now()", p.OrganizationID, original, key); e != nil {
			return e
		}
		var hash []byte
		var newID string
		var purged bool
		e := tx.QueryRowContext(ctx, `SELECT r.request_hash,r.new_delivery_id,e.envelope_bytes IS NULL FROM replay_requests r JOIN deliveries d ON d.id=r.original_delivery_id JOIN events e ON e.id=d.event_id WHERE r.organization_id=$1 AND r.project_id=$2 AND r.original_delivery_id=$3 AND r.key=$4`, p.OrganizationID, project, original, key).Scan(&hash, &newID, &purged)
		if e == nil {
			if !bytes.Equal(hash, security.Hash(string(raw))) {
				return model.Fail(409, "idempotency_conflict", "Replay request bytes changed")
			}
			if purged {
				return model.Fail(410, "payload_purged", "Event payload has expired")
			}
			var generation int
			var eventID string
			if e = tx.QueryRowContext(ctx, "SELECT generation,event_id FROM deliveries WHERE id=$1", newID).Scan(&generation, &eventID); e != nil {
				return e
			}
			result = map[string]any{"delivery_id": newID, "event_id": eventID, "generation": generation, "idempotency_replayed": true}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		if e = lock(ctx, tx, p.OrganizationID, project); e != nil {
			return e
		}
		var endpoint, version, event, state string
		e = tx.QueryRowContext(ctx, "SELECT endpoint_id,endpoint_version_id,event_id,state FROM deliveries WHERE organization_id=$1 AND project_id=$2 AND id=$3", p.OrganizationID, project, original).Scan(&endpoint, &version, &event, &state)
		if e == sql.ErrNoRows {
			return missing()
		}
		if e != nil {
			return e
		}
		if state != "succeeded" && state != "failed" && state != "canceled" {
			return model.Fail(409, "delivery_active", "Only terminal deliveries can be replayed")
		}
		var paused bool
		if e = tx.QueryRowContext(ctx, "SELECT paused FROM endpoints WHERE id=$1 FOR UPDATE", endpoint).Scan(&paused); e != nil {
			return e
		}
		if paused {
			return model.Fail(409, "endpoint_paused", "Endpoint is paused")
		}
		if e = tx.QueryRowContext(ctx, "SELECT envelope_bytes IS NULL FROM events WHERE id=$1", event).Scan(&purged); e != nil {
			return e
		}
		if purged {
			return model.Fail(410, "payload_purged", "Event payload has expired")
		}
		var active bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM deliveries WHERE event_id=$1 AND endpoint_id=$2 AND state IN('pending','retry_wait','in_flight'))", event, endpoint).Scan(&active); e != nil {
			return e
		}
		if active {
			return model.Fail(409, "delivery_active", "An active generation already exists")
		}
		if e = reserve(ctx, tx, s, p.OrganizationID, 1); e != nil {
			return e
		}
		newID = ID()
		if _, e = tx.ExecContext(ctx, `INSERT INTO deliveries(id,organization_id,project_id,event_id,endpoint_id,endpoint_version_id,generation,state,next_attempt_at,deadline_at) SELECT $1,$2,$3,$4,$5,$6,COALESCE(max(generation),0)+1,'pending',now(),now()+interval '24 hours' FROM deliveries WHERE event_id=$4 AND endpoint_id=$5`, newID, p.OrganizationID, project, event, endpoint, version); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO replay_requests(id,organization_id,project_id,original_delivery_id,key,request_hash,new_delivery_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, ID(), p.OrganizationID, project, original, key, security.Hash(string(raw)), newID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE events SET retention_at=NULL WHERE id=$1", event); e != nil {
			return e
		}
		var generation int
		if e = tx.QueryRowContext(ctx, "SELECT generation FROM deliveries WHERE id=$1", newID).Scan(&generation); e != nil {
			return e
		}
		result = map[string]any{"delivery_id": newID, "event_id": event, "generation": generation, "idempotency_replayed": false}
		return audit(ctx, tx, p, "delivery.replayed", "delivery", newID)
	})
	return result, e
}
