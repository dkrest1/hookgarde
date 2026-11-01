package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
)

func (s *Store) Project(ctx context.Context, org, id string) (model.Project, error) {
	var p model.Project
	e := s.DB.QueryRowContext(ctx, "SELECT id,organization_id,name,created_at,archived_at FROM projects WHERE organization_id=$1 AND id=$2", org, id).Scan(&p.ID, &p.OrganizationID, &p.Name, &p.CreatedAt, &p.ArchivedAt)
	if e == sql.ErrNoRows {
		return p, missing()
	}
	return p, e
}
func (s *Store) CreateProject(ctx context.Context, p model.Principal, name string) (model.Project, error) {
	v := model.Project{ID: ID(), OrganizationID: p.OrganizationID, Name: strings.TrimSpace(name)}
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, ""); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE organization_id=$1 AND archived_at IS NULL", p.OrganizationID).Scan(&n); e != nil {
			return e
		}
		if n >= s.Config.Projects {
			return model.Fail(409, "resource_limit", "Project limit reached")
		}
		if e := tx.QueryRowContext(ctx, "INSERT INTO projects(id,organization_id,name) VALUES($1,$2,$3) RETURNING created_at", v.ID, p.OrganizationID, v.Name).Scan(&v.CreatedAt); e != nil {
			return e
		}
		return audit(ctx, tx, p, "project.created", "project", v.ID)
	})
	return v, e
}
func (s *Store) UpdateProject(ctx context.Context, p model.Principal, id string, name *string, archive *bool) (model.Project, error) {
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, ""); e != nil {
			return e
		}
		var archived *time.Time
		if e := tx.QueryRowContext(ctx, "SELECT archived_at FROM projects WHERE organization_id=$1 AND id=$2 FOR UPDATE", p.OrganizationID, id).Scan(&archived); e == sql.ErrNoRows {
			return missing()
		} else if e != nil {
			return e
		}
		if archive != nil && !*archive {
			return model.Fail(422, "invalid_archive", "Unarchiving is not supported")
		}
		if name != nil {
			if _, e := tx.ExecContext(ctx, "UPDATE projects SET name=$3 WHERE organization_id=$1 AND id=$2", p.OrganizationID, id, strings.TrimSpace(*name)); e != nil {
				return e
			}
		}
		if archive != nil && *archive {
			if _, e := tx.ExecContext(ctx, "UPDATE projects SET archived_at=COALESCE(archived_at,now()) WHERE organization_id=$1 AND id=$2", p.OrganizationID, id); e != nil {
				return e
			}
			result, e := tx.ExecContext(ctx, `UPDATE deliveries SET state='canceled',terminal_at=now(),next_attempt_at=NULL,last_error='project_archived' WHERE organization_id=$1 AND project_id=$2 AND state IN('pending','retry_wait')`, p.OrganizationID, id)
			if e != nil {
				return e
			}
			n, _ := result.RowsAffected()
			if e = release(ctx, tx, p.OrganizationID, int(n)); e != nil {
				return e
			}
		}
		return audit(ctx, tx, p, "project.updated", "project", id)
	})
	if e != nil {
		return model.Project{}, e
	}
	return s.Project(ctx, p.OrganizationID, id)
}

const endpointSelect = `SELECT e.id,e.project_id,v.url,e.description,e.paused,v.version,e.created_at FROM endpoints e JOIN endpoint_versions v ON v.id=e.current_version_id`

func scanEndpoint(row interface{ Scan(...any) error }) (model.Endpoint, error) {
	var v model.Endpoint
	e := row.Scan(&v.ID, &v.ProjectID, &v.URL, &v.Description, &v.Paused, &v.Version, &v.CreatedAt)
	return v, e
}
func (s *Store) Endpoint(ctx context.Context, org, project, id string) (model.Endpoint, error) {
	v, e := scanEndpoint(s.DB.QueryRowContext(ctx, endpointSelect+" WHERE e.organization_id=$1 AND e.project_id=$2 AND e.id=$3", org, project, id))
	if e == sql.ErrNoRows {
		return v, missing()
	}
	return v, e
}
func (s *Store) newSigningKey(ctx context.Context, tx *sql.Tx, p model.Principal, project, endpoint string, emergency, initial bool) (string, string, int, time.Time, error) {
	var version int
	if e := tx.QueryRowContext(ctx, "SELECT COALESCE(max(version),0)+1 FROM signing_keys WHERE endpoint_id=$1", endpoint).Scan(&version); e != nil {
		return "", "", 0, time.Time{}, e
	}
	secret, id := security.Random(), ID()
	aad := p.OrganizationID + ":" + project + ":" + endpoint + ":" + id
	encrypted, e := security.Encrypt(s.Config.EncryptionKey, secret, aad)
	if e != nil {
		return "", "", 0, time.Time{}, e
	}
	now := time.Now().UTC()
	activation := now.Add(10 * time.Minute)
	if initial || emergency {
		activation = now
	}
	if !initial {
		if emergency {
			_, e = tx.ExecContext(ctx, "UPDATE signing_keys SET revoked_at=now() WHERE endpoint_id=$1 AND revoked_at IS NULL", endpoint)
		} else {
			var pending int
			e = tx.QueryRowContext(ctx, "SELECT count(*) FROM signing_keys WHERE endpoint_id=$1 AND activates_at>now() AND revoked_at IS NULL", endpoint).Scan(&pending)
			if pending > 0 {
				return "", "", 0, time.Time{}, model.Fail(409, "rotation_pending", "A signing rotation is already pending")
			}
			if e == nil {
				_, e = tx.ExecContext(ctx, "UPDATE signing_keys SET retires_at=$2 WHERE endpoint_id=$1 AND revoked_at IS NULL AND retires_at IS NULL", endpoint, activation.Add(24*time.Hour))
			}
		}
		if e != nil {
			return "", "", 0, time.Time{}, e
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO signing_keys(id,organization_id,project_id,endpoint_id,version,ciphertext,activates_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, p.OrganizationID, project, endpoint, version, encrypted, activation)
	return secret, id, version, activation, e
}
func (s *Store) CreateEndpoint(ctx context.Context, p model.Principal, project, url, description string) (model.Endpoint, error) {
	v := model.Endpoint{ID: ID(), ProjectID: project, URL: url, Description: description, Version: 1}
	versionID := ID()
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, project); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM endpoints e JOIN projects p ON p.id=e.project_id WHERE e.organization_id=$1 AND p.archived_at IS NULL`, p.OrganizationID).Scan(&n); e != nil {
			return e
		}
		if n >= s.Config.Endpoints {
			return model.Fail(409, "resource_limit", "Endpoint limit reached")
		}
		if e := tx.QueryRowContext(ctx, `INSERT INTO endpoints(id,organization_id,project_id,description,current_version_id) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, v.ID, p.OrganizationID, project, description, versionID).Scan(&v.CreatedAt); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO endpoint_versions(id,organization_id,project_id,endpoint_id,version,url) VALUES($1,$2,$3,$4,1,$5)`, versionID, p.OrganizationID, project, v.ID, url); e != nil {
			return e
		}
		secret, _, _, _, e := s.newSigningKey(ctx, tx, p, project, v.ID, false, true)
		if e != nil {
			return e
		}
		v.SigningSecret = secret
		return audit(ctx, tx, p, "endpoint.created", "endpoint", v.ID)
	})
	return v, e
}
func (s *Store) UpdateEndpoint(ctx context.Context, p model.Principal, project, id string, url, description *string, paused *bool) (model.Endpoint, error) {
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, project); e != nil {
			return e
		}
		var version int
		e := tx.QueryRowContext(ctx, `SELECT v.version FROM endpoints e JOIN endpoint_versions v ON v.id=e.current_version_id WHERE e.organization_id=$1 AND e.project_id=$2 AND e.id=$3 FOR UPDATE OF e`, p.OrganizationID, project, id).Scan(&version)
		if e == sql.ErrNoRows {
			return missing()
		}
		if e != nil {
			return e
		}
		if url != nil {
			vid := ID()
			if _, e = tx.ExecContext(ctx, `INSERT INTO endpoint_versions(id,organization_id,project_id,endpoint_id,version,url) VALUES($1,$2,$3,$4,$5,$6)`, vid, p.OrganizationID, project, id, version+1, *url); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE endpoints SET current_version_id=$2 WHERE id=$1", id, vid); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE endpoints SET description=COALESCE($2,description),paused=COALESCE($3,paused) WHERE id=$1", id, description, paused); e != nil {
			return e
		}
		return audit(ctx, tx, p, "endpoint.updated", "endpoint", id)
	})
	if e != nil {
		return model.Endpoint{}, e
	}
	return s.Endpoint(ctx, p.OrganizationID, project, id)
}
func (s *Store) Rotate(ctx context.Context, p model.Principal, project, id string, emergency bool) (map[string]any, error) {
	var result map[string]any
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, project); e != nil {
			return e
		}
		var found string
		e := tx.QueryRowContext(ctx, "SELECT id FROM endpoints WHERE organization_id=$1 AND project_id=$2 AND id=$3 FOR UPDATE", p.OrganizationID, project, id).Scan(&found)
		if e == sql.ErrNoRows {
			return missing()
		}
		if e != nil {
			return e
		}
		secret, _, version, activation, e := s.newSigningKey(ctx, tx, p, project, id, emergency, false)
		if e != nil {
			return e
		}
		result = map[string]any{"key_version": version, "secret": secret, "activates_at": activation, "previous_retires_at": nil}
		if !emergency {
			result["previous_retires_at"] = activation.Add(24 * time.Hour)
		}
		return audit(ctx, tx, p, "signing_key.rotated", "endpoint", id)
	})
	return result, e
}
func (s *Store) CreateKey(ctx context.Context, p model.Principal, project, name string, expiry *time.Time) (model.Key, error) {
	token := "hg_" + security.Random()
	v := model.Key{ID: ID(), ProjectID: project, Name: strings.TrimSpace(name), Prefix: token[:11], Scope: "events:write", Token: token, ExpiresAt: expiry}
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, project); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, `INSERT INTO api_keys(id,organization_id,project_id,name,prefix,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, v.ID, p.OrganizationID, project, v.Name, v.Prefix, security.Hash(token), expiry).Scan(&v.CreatedAt); e != nil {
			return e
		}
		return audit(ctx, tx, p, "api_key.created", "api_key", v.ID)
	})
	return v, e
}
func (s *Store) RevokeKey(ctx context.Context, p model.Principal, project, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if e := lock(ctx, tx, p.OrganizationID, ""); e != nil {
			return e
		}
		r, e := tx.ExecContext(ctx, "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE organization_id=$1 AND project_id=$2 AND id=$3", p.OrganizationID, project, id)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return missing()
		}
		return audit(ctx, tx, p, "api_key.revoked", "api_key", id)
	})
}
