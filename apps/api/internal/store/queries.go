package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/google/uuid"
)

type Page struct {
	Limit    int
	Time     time.Time
	ID       string
	Sequence int
}

func ParsePage(limit, cursor string) (Page, error) {
	p := Page{Limit: 25, Time: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}
	if limit != "" {
		n, e := strconv.Atoi(limit)
		if e != nil || n < 1 || n > 100 {
			return p, model.Fail(400, "invalid_limit", "Limit must be between 1 and 100")
		}
		p.Limit = n
	}
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return p, model.Fail(400, "invalid_cursor", "Invalid cursor")
		}
		parts := strings.Split(string(b), "|")
		if len(parts) != 3 {
			return p, model.Fail(400, "invalid_cursor", "Invalid cursor")
		}
		p.Time, e = time.Parse(time.RFC3339Nano, parts[0])
		if e != nil {
			return p, model.Fail(400, "invalid_cursor", "Invalid cursor")
		}
		if _, e = uuid.Parse(parts[1]); e != nil {
			return p, model.Fail(400, "invalid_cursor", "Invalid cursor")
		}
		p.ID = parts[1]
		p.Sequence, e = strconv.Atoi(parts[2])
		if e != nil || p.Sequence < 0 {
			return p, model.Fail(400, "invalid_cursor", "Invalid cursor")
		}
	}
	return p, nil
}
func cursor(t time.Time, id string, sequence int) *string {
	v := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s|%s|%d", t.UTC().Format(time.RFC3339Nano), id, sequence)))
	return &v
}
func (s *Store) Projects(ctx context.Context, org string, p Page) (model.List, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,organization_id,name,created_at,archived_at FROM projects WHERE organization_id=$1 AND (created_at,id)<($2,$3::uuid) ORDER BY created_at DESC,id DESC LIMIT $4", org, p.Time, p.ID, p.Limit+1)
	if e != nil {
		return model.List{}, e
	}
	defer rows.Close()
	items := []model.Project{}
	for rows.Next() {
		var v model.Project
		if e = rows.Scan(&v.ID, &v.OrganizationID, &v.Name, &v.CreatedAt, &v.ArchivedAt); e != nil {
			return model.List{}, e
		}
		items = append(items, v)
	}
	var next *string
	if len(items) > p.Limit {
		v := items[p.Limit-1]
		next = cursor(v.CreatedAt, v.ID, 0)
		items = items[:p.Limit]
	}
	return model.List{Data: items, NextCursor: next}, rows.Err()
}
func (s *Store) Endpoints(ctx context.Context, org, project string, p Page) (model.List, error) {
	if _, e := s.Project(ctx, org, project); e != nil {
		return model.List{}, e
	}
	rows, e := s.DB.QueryContext(ctx, endpointSelect+" WHERE e.organization_id=$1 AND e.project_id=$2 AND (e.created_at,e.id)<($3,$4::uuid) ORDER BY e.created_at DESC,e.id DESC LIMIT $5", org, project, p.Time, p.ID, p.Limit+1)
	if e != nil {
		return model.List{}, e
	}
	defer rows.Close()
	items := []model.Endpoint{}
	for rows.Next() {
		v, e := scanEndpoint(rows)
		if e != nil {
			return model.List{}, e
		}
		items = append(items, v)
	}
	var next *string
	if len(items) > p.Limit {
		v := items[p.Limit-1]
		next = cursor(v.CreatedAt, v.ID, 0)
		items = items[:p.Limit]
	}
	return model.List{Data: items, NextCursor: next}, rows.Err()
}
func (s *Store) Keys(ctx context.Context, org, project string, p Page) (model.List, error) {
	if _, e := s.Project(ctx, org, project); e != nil {
		return model.List{}, e
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT id,project_id,name,prefix,scope,created_at,expires_at,revoked_at FROM api_keys WHERE organization_id=$1 AND project_id=$2 AND (created_at,id)<($3,$4::uuid) ORDER BY created_at DESC,id DESC LIMIT $5", org, project, p.Time, p.ID, p.Limit+1)
	if e != nil {
		return model.List{}, e
	}
	defer rows.Close()
	items := []model.Key{}
	for rows.Next() {
		var v model.Key
		if e = rows.Scan(&v.ID, &v.ProjectID, &v.Name, &v.Prefix, &v.Scope, &v.CreatedAt, &v.ExpiresAt, &v.RevokedAt); e != nil {
			return model.List{}, e
		}
		items = append(items, v)
	}
	var next *string
	if len(items) > p.Limit {
		v := items[p.Limit-1]
		next = cursor(v.CreatedAt, v.ID, 0)
		items = items[:p.Limit]
	}
	return model.List{Data: items, NextCursor: next}, rows.Err()
}

const eventStatus = `CASE WHEN EXISTS(SELECT 1 FROM deliveries d WHERE d.event_id=e.id AND d.state IN('pending','retry_wait','in_flight')) THEN 'pending' WHEN EXISTS(SELECT 1 FROM deliveries d WHERE d.event_id=e.id AND d.state IN('failed','canceled')) THEN 'completed_with_failures' ELSE 'completed' END`

func (s *Store) Events(ctx context.Context, org, project string, p Page, kind, status string, from, to time.Time) (model.List, error) {
	if _, e := s.Project(ctx, org, project); e != nil {
		return model.List{}, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT e.id,e.type,e.accepted_at,`+eventStatus+`,e.payload_purged_at FROM events e WHERE organization_id=$1 AND project_id=$2 AND (accepted_at,id)<($3,$4::uuid) AND ($5='' OR type=$5) AND accepted_at>=$6 AND accepted_at<=$7 AND ($8='' OR (`+eventStatus+`)=$8) ORDER BY accepted_at DESC,id DESC LIMIT $9`, org, project, p.Time, p.ID, kind, from, to, status, p.Limit+1)
	if e != nil {
		return model.List{}, e
	}
	defer rows.Close()
	items := []model.Event{}
	for rows.Next() {
		var v model.Event
		if e = rows.Scan(&v.ID, &v.Type, &v.AcceptedAt, &v.Status, &v.PayloadPurgedAt); e != nil {
			return model.List{}, e
		}
		items = append(items, v)
	}
	var next *string
	if len(items) > p.Limit {
		v := items[p.Limit-1]
		next = cursor(v.AcceptedAt, v.ID, 0)
		items = items[:p.Limit]
	}
	return model.List{Data: items, NextCursor: next}, rows.Err()
}
func (s *Store) Event(ctx context.Context, org, project, id string) (map[string]any, error) {
	var v model.Event
	var body []byte
	e := s.DB.QueryRowContext(ctx, `SELECT e.id,e.type,e.accepted_at,`+eventStatus+`,e.envelope_bytes,e.payload_purged_at FROM events e WHERE organization_id=$1 AND project_id=$2 AND id=$3`, org, project, id).Scan(&v.ID, &v.Type, &v.AcceptedAt, &v.Status, &body, &v.PayloadPurgedAt)
	if e == sql.ErrNoRows {
		return nil, missing()
	}
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT d.id,d.event_id,d.endpoint_id,d.generation,d.state,d.attempts_count,d.last_error,d.deadline_at,d.next_attempt_at,ep.paused,v.version,d.created_at FROM deliveries d JOIN endpoints ep ON ep.id=d.endpoint_id JOIN endpoint_versions v ON v.id=d.endpoint_version_id WHERE d.organization_id=$1 AND d.project_id=$2 AND d.event_id=$3 ORDER BY d.created_at,d.id`, org, project, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []model.Delivery{}
	for rows.Next() {
		var d model.Delivery
		if e = rows.Scan(&d.ID, &d.EventID, &d.EndpointID, &d.Generation, &d.State, &d.AttemptsCount, &d.LastError, &d.DeadlineAt, &d.NextAttemptAt, &d.Paused, &d.EndpointVersion, &d.CreatedAt); e != nil {
			return nil, e
		}
		items = append(items, d)
	}
	var envelope any
	if body != nil {
		envelope = json.RawMessage(body)
	}
	return map[string]any{"event": v, "envelope": envelope, "payload_purged_at": v.PayloadPurgedAt, "deliveries": items}, rows.Err()
}
func (s *Store) Attempts(ctx context.Context, org, project, id string, p Page) (model.List, error) {
	var found string
	e := s.DB.QueryRowContext(ctx, "SELECT id FROM deliveries WHERE organization_id=$1 AND project_id=$2 AND id=$3", org, project, id).Scan(&found)
	if e == sql.ErrNoRows {
		return model.List{}, missing()
	}
	if e != nil {
		return model.List{}, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id,sequence,started_at,ended_at,outcome,http_status,duration_ms,error_class,response_preview FROM delivery_attempts WHERE organization_id=$1 AND project_id=$2 AND delivery_id=$3 AND sequence>$4 ORDER BY sequence,id LIMIT $5`, org, project, id, p.Sequence, p.Limit+1)
	if e != nil {
		return model.List{}, e
	}
	defer rows.Close()
	items := []model.Attempt{}
	for rows.Next() {
		var v model.Attempt
		if e = rows.Scan(&v.ID, &v.Sequence, &v.StartedAt, &v.EndedAt, &v.Outcome, &v.HTTPStatus, &v.DurationMS, &v.ErrorClass, &v.ResponsePreview); e != nil {
			return model.List{}, e
		}
		items = append(items, v)
	}
	var next *string
	if len(items) > p.Limit {
		v := items[p.Limit-1]
		next = cursor(v.StartedAt, v.ID, v.Sequence)
		items = items[:p.Limit]
	}
	return model.List{Data: items, NextCursor: next}, rows.Err()
}
func (s *Store) Usage(ctx context.Context, org string) (map[string]any, error) {
	var count int
	var active int
	e := s.DB.QueryRowContext(ctx, `SELECT o.active_jobs,COALESCE(u.accepted_event_count,0) FROM organizations o LEFT JOIN usage_months u ON u.organization_id=o.id AND u.month_start=date_trunc('month',now() AT TIME ZONE 'UTC')::date WHERE o.id=$1`, org).Scan(&active, &count)
	now := time.Now().UTC()
	reset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return map[string]any{"month_start": now.Format("2006-01") + "-01", "accepted_event_count": count, "limits": map[string]int{"projects": s.Config.Projects, "active_endpoints": s.Config.Endpoints, "targets_per_event": s.Config.Targets, "events_per_month": s.Config.MonthlyEvents}, "reset_at": reset}, e
}
