package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
)

// Claim reserves one attempt and commits its lease before any receiver I/O.
// excluded endpoints already have two sends in this worker process.
func (s *Store) Claim(ctx context.Context, excluded []string) (*model.Claim, error) {
	var c *model.Claim
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		var capacity string
		if e := tx.QueryRowContext(ctx, "SELECT key FROM system_capacity WHERE key='delivery_queue' FOR UPDATE").Scan(&capacity); e != nil {
			return e
		}
		v := &model.Claim{}
		e := tx.QueryRowContext(ctx, `SELECT d.id,d.organization_id,d.project_id,d.endpoint_id FROM deliveries d JOIN organizations o ON o.id=d.organization_id JOIN projects p ON p.id=d.project_id JOIN endpoints ep ON ep.id=d.endpoint_id WHERE d.state IN('pending','retry_wait') AND d.next_attempt_at<=now() AND d.deadline_at>now() AND p.archived_at IS NULL AND NOT ep.paused AND NOT(d.endpoint_id::text=ANY($1::text[])) ORDER BY o.last_claim_at,d.next_attempt_at,d.id LIMIT 1`, excluded).Scan(&v.DeliveryID, &v.OrganizationID, &v.ProjectID, &v.EndpointID)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		if e = lock(ctx, tx, v.OrganizationID, v.ProjectID); e != nil {
			return e
		}
		var paused bool
		if e = tx.QueryRowContext(ctx, "SELECT paused FROM endpoints WHERE id=$1 FOR UPDATE", v.EndpointID).Scan(&paused); e != nil {
			return e
		}
		if paused {
			return nil
		}
		e = tx.QueryRowContext(ctx, `SELECT d.event_id,d.endpoint_version_id,d.attempts_count+1,d.deadline_at,e.envelope_bytes,v.url FROM deliveries d JOIN events e ON e.id=d.event_id JOIN endpoint_versions v ON v.id=d.endpoint_version_id WHERE d.id=$1 AND d.state IN('pending','retry_wait') AND d.next_attempt_at<=now() AND d.deadline_at>now() FOR UPDATE OF d SKIP LOCKED`, v.DeliveryID).Scan(&v.EventID, &v.VersionID, &v.Sequence, &v.Deadline, &v.Body, &v.URL)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, `SELECT id,version,ciphertext FROM signing_keys WHERE endpoint_id=$1 AND activates_at<=now() AND (retires_at IS NULL OR retires_at>now()) AND revoked_at IS NULL ORDER BY version DESC LIMIT 1`, v.EndpointID).Scan(&v.SigningKeyID, &v.KeyVersion, &v.Ciphertext)
		if e != nil {
			return fmt.Errorf("select active signing key: %w", e)
		}
		v.Token = ID()
		v.AttemptID = ID()
		if _, e = tx.ExecContext(ctx, `UPDATE deliveries SET state='in_flight',attempts_count=attempts_count+1,lease_token=$2,lease_until=now()+interval '60 seconds',next_attempt_at=NULL WHERE id=$1`, v.DeliveryID, v.Token); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO delivery_attempts(id,organization_id,project_id,delivery_id,sequence,lease_token,signing_key_id,outcome) VALUES($1,$2,$3,$4,$5,$6,$7,'started')`, v.AttemptID, v.OrganizationID, v.ProjectID, v.DeliveryID, v.Sequence, v.Token, v.SigningKeyID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE organizations SET last_claim_at=now() WHERE id=$1", v.OrganizationID); e != nil {
			return e
		}
		c = v
		return nil
	})
	return c, e
}

// Complete is fenced: an expired worker cannot overwrite recovery evidence.
func (s *Store) Complete(ctx context.Context, c model.Claim, o model.Outcome) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, c.OrganizationID, ""); e != nil {
			return e
		}
		var archive *time.Time
		if e := tx.QueryRowContext(ctx, "SELECT archived_at FROM projects WHERE id=$1 FOR UPDATE", c.ProjectID).Scan(&archive); e != nil {
			return e
		}
		var endpoint string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM endpoints WHERE id=$1 FOR UPDATE", c.EndpointID).Scan(&endpoint); e != nil {
			return e
		}
		var token string
		var deadline time.Time
		e := tx.QueryRowContext(ctx, "SELECT COALESCE(lease_token::text,''),deadline_at FROM deliveries WHERE id=$1 AND state='in_flight' FOR UPDATE", c.DeliveryID).Scan(&token, &deadline)
		if e == sql.ErrNoRows || token != c.Token {
			return nil
		}
		if e != nil {
			return e
		}
		state, outcome := "failed", "failed"
		if o.Success {
			state = "succeeded"
			outcome = "succeeded"
		} else if archive != nil {
			state = "canceled"
			o.ErrorClass = "project_archived"
		} else if o.Retry && c.Sequence < 6 && time.Now().Add(o.Delay).Before(deadline) {
			state = "retry_wait"
		}
		var next *time.Time
		var terminal *time.Time
		now := time.Now().UTC()
		if state == "retry_wait" {
			t := now.Add(o.Delay)
			next = &t
		} else {
			terminal = &now
		}
		if _, e = tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome=$2,ended_at=now(),http_status=NULLIF($3,0),duration_ms=$4,error_class=$5,response_preview=$6 WHERE id=$1 AND lease_token=$7 AND outcome='started'`, c.AttemptID, outcome, o.Status, o.DurationMS, o.ErrorClass, o.Preview, c.Token); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE deliveries SET state=$2,next_attempt_at=$3,terminal_at=$4,last_error=$5,lease_token=NULL,lease_until=NULL WHERE id=$1`, c.DeliveryID, state, next, terminal, o.ErrorClass); e != nil {
			return e
		}
		if terminal != nil {
			return release(ctx, tx, c.OrganizationID, 1)
		}
		return nil
	})
}
func (s *Store) Maintain(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var key string
		if e := tx.QueryRowContext(ctx, "SELECT key FROM system_capacity WHERE key='delivery_queue' FOR UPDATE").Scan(&key); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,organization_id,lease_token::text,attempts_count,deadline_at,state FROM deliveries WHERE (state='in_flight' AND lease_until<=now()) OR (state IN('pending','retry_wait') AND deadline_at<=now()) ORDER BY deadline_at,id LIMIT 100 FOR UPDATE`)
		if e != nil {
			return e
		}
		type expired struct {
			id, org  string
			token    *string
			count    int
			deadline time.Time
			state    string
		}
		items := []expired{}
		for rows.Next() {
			var v expired
			if e = rows.Scan(&v.id, &v.org, &v.token, &v.count, &v.deadline, &v.state); e != nil {
				rows.Close()
				return e
			}
			items = append(items, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, v := range items {
			state := "retry_wait"
			delay := 10 * time.Second
			for n := 1; n < v.count; n++ {
				delay *= 4
			}
			if delay > 2*time.Hour {
				delay = 2 * time.Hour
			}
			next := time.Now().UTC().Add(delay)
			var terminal *time.Time
			var nextAt *time.Time = &next
			reason := "lease_expired_unknown"
			var archived bool
			if e = tx.QueryRowContext(ctx, "SELECT p.archived_at IS NOT NULL FROM deliveries d JOIN projects p ON p.id=d.project_id WHERE d.id=$1", v.id).Scan(&archived); e != nil {
				return e
			}
			if archived {
				state = "canceled"
				reason = "project_archived"
			} else if v.count >= 6 || !next.Before(v.deadline) {
				state = "failed"
				reason = "attempt_budget_or_deadline_exhausted"
			}
			if state != "retry_wait" {
				now := time.Now().UTC()
				terminal = &now
				nextAt = nil
			}
			if v.token != nil {
				if _, e = tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='unknown',ended_at=now(),error_class='lease_expired_unknown' WHERE delivery_id=$1 AND lease_token=$2::uuid AND outcome='started'`, v.id, *v.token); e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, `UPDATE deliveries SET state=$2,next_attempt_at=$3,terminal_at=$4,last_error=$5,lease_token=NULL,lease_until=NULL WHERE id=$1`, v.id, state, nextAt, terminal, reason); e != nil {
				return e
			}
			if terminal != nil {
				if e = release(ctx, tx, v.org, 1); e != nil {
					return e
				}
			}
		}
		// Retain active bodies regardless of age; terminal payload retention starts
		// only once every generation is terminal. Metadata remains for dedupe.
		if _, e = tx.ExecContext(ctx, `UPDATE events e SET retention_at=(SELECT max(terminal_at)+interval '7 days' FROM deliveries d WHERE d.event_id=e.id) WHERE retention_at IS NULL AND NOT EXISTS(SELECT 1 FROM deliveries d WHERE d.event_id=e.id AND d.state IN('pending','retry_wait','in_flight'))`); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `WITH due AS(SELECT id FROM events WHERE retention_at<=now() AND envelope_bytes IS NOT NULL LIMIT 100) UPDATE events SET envelope_bytes=NULL,payload_purged_at=now() WHERE id IN(SELECT id FROM due)`); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE delivery_attempts a SET response_preview='' FROM deliveries d JOIN events e ON e.id=d.event_id WHERE a.delivery_id=d.id AND e.payload_purged_at IS NOT NULL AND a.response_preview<>''`); e != nil {
			return e
		}
		for _, q := range []string{`DELETE FROM rate_limit_buckets WHERE expires_at<now()`, `DELETE FROM sessions WHERE expires_at<now()-interval '1 day'`, `DELETE FROM audit_entries WHERE occurred_at<now()-interval '90 days'`, `DELETE FROM ingestion_requests WHERE expires_at<now()`, `DELETE FROM replay_requests WHERE expires_at<now()`} {
			if _, e = tx.ExecContext(ctx, q); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Reconcile(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var key string
		if e := tx.QueryRowContext(ctx, "SELECT key FROM system_capacity WHERE key='delivery_queue' FOR UPDATE").Scan(&key); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE organizations o SET active_jobs=(SELECT count(*) FROM deliveries d WHERE d.organization_id=o.id AND state IN('pending','retry_wait','in_flight'))`); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE system_capacity SET active_jobs=(SELECT count(*) FROM deliveries WHERE state IN('pending','retry_wait','in_flight')),updated_at=now() WHERE key='delivery_queue'`)
		return e
	})
}
func (s *Store) Metrics(ctx context.Context) (string, error) {
	var active, failed, succeeded, unknown int64
	var age float64
	e := s.DB.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state IN('pending','retry_wait','in_flight')),count(*) FILTER(WHERE state='failed'),count(*) FILTER(WHERE state='succeeded'),COALESCE(extract(epoch FROM now()-min(next_attempt_at) FILTER(WHERE state IN('pending','retry_wait') AND next_attempt_at<=now())),0) FROM deliveries`).Scan(&active, &failed, &succeeded, &age)
	if e != nil {
		return "", e
	}
	if e = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM delivery_attempts WHERE outcome='unknown'").Scan(&unknown); e != nil {
		return "", e
	}
	return fmt.Sprintf("# TYPE hookgarde_active_jobs gauge\nhookgarde_active_jobs %d\n# TYPE hookgarde_failed_jobs gauge\nhookgarde_failed_jobs %d\n# TYPE hookgarde_succeeded_jobs gauge\nhookgarde_succeeded_jobs %d\n# TYPE hookgarde_unknown_attempts gauge\nhookgarde_unknown_attempts %d\n# TYPE hookgarde_oldest_due_seconds gauge\nhookgarde_oldest_due_seconds %f\n", active, failed, succeeded, unknown, age), nil
}
