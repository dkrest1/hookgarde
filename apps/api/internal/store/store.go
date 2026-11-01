// Package store owns PostgreSQL transactions. All state-changing delivery paths
// lock global capacity -> organization -> project -> endpoint -> job. Holding
// these policy locks is deliberately conservative for the self-hosted alpha.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrations embed.FS

const SchemaVersion = 1

type Store struct {
	DB     *sql.DB
	Config config.Config
}

func Open(ctx context.Context, c config.Config) (*Store, error) {
	db, e := sql.Open("pgx", c.DatabaseURL)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db, c}, nil
}
func ID() string { return uuid.NewString() }
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(48474201)"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		return e
	}
	body, e := migrations.ReadFile("migrations/001_core.sql")
	if e != nil {
		return e
	}
	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])
	var existing string
	e = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=1").Scan(&existing)
	if e == sql.ErrNoRows {
		if _, e = tx.ExecContext(ctx, string(body)); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES(1,$1)", checksum)
	} else if e == nil && existing != checksum {
		return fmt.Errorf("migration checksum mismatch")
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Ready(ctx context.Context) error {
	var n int
	if e := s.DB.QueryRowContext(ctx, "SELECT COALESCE(max(version),0) FROM schema_migrations").Scan(&n); e != nil {
		return e
	}
	if n != SchemaVersion {
		return fmt.Errorf("incompatible database schema")
	}
	return nil
}
func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SET LOCAL lock_timeout='3s'"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "SET LOCAL statement_timeout='10s'"); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func lock(ctx context.Context, tx *sql.Tx, org, project string) error {
	var id string
	if e := tx.QueryRowContext(ctx, "SELECT key FROM system_capacity WHERE key='delivery_queue' FOR UPDATE").Scan(&id); e != nil {
		return e
	}
	if e := tx.QueryRowContext(ctx, "SELECT id FROM organizations WHERE id=$1 FOR UPDATE", org).Scan(&id); e != nil {
		return e
	}
	if project != "" {
		var archived *time.Time
		e := tx.QueryRowContext(ctx, "SELECT id,archived_at FROM projects WHERE organization_id=$1 AND id=$2 FOR UPDATE", org, project).Scan(&id, &archived)
		if e == sql.ErrNoRows {
			return missing()
		}
		if e != nil {
			return e
		}
		if archived != nil {
			return model.Fail(409, "project_archived", "Project is archived")
		}
	}
	return nil
}
func missing() error { return model.Fail(404, "not_found", "Resource not found") }
func audit(ctx context.Context, tx *sql.Tx, p model.Principal, action, kind, id string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO audit_entries(id,organization_id,actor_user_id,actor_key_id,action,resource_type,resource_id) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7)`, ID(), p.OrganizationID, p.UserID, p.KeyID, action, kind, id)
	return e
}
func reserve(ctx context.Context, tx *sql.Tx, s *Store, org string, n int) error {
	var ok int
	e := tx.QueryRowContext(ctx, `UPDATE system_capacity SET active_jobs=active_jobs+$1,capacity=$2,updated_at=now() WHERE key='delivery_queue' AND active_jobs+$1<=$2 RETURNING 1`, n, s.Config.QueueJobs).Scan(&ok)
	if e == sql.ErrNoRows {
		return model.Fail(503, "queue_capacity_exceeded", "Queue capacity exhausted")
	}
	if e != nil {
		return e
	}
	e = tx.QueryRowContext(ctx, `UPDATE organizations SET active_jobs=active_jobs+$2 WHERE id=$1 AND active_jobs+$2<=$3 RETURNING 1`, org, n, s.Config.OrganizationJobs).Scan(&ok)
	if e == sql.ErrNoRows {
		return model.Fail(503, "queue_capacity_exceeded", "Organization queue capacity exhausted")
	}
	if e != nil {
		return e
	}
	var size int64
	if e = tx.QueryRowContext(ctx, "SELECT pg_database_size(current_database())").Scan(&size); e != nil {
		return e
	}
	if size >= s.Config.StorageLimitBytes-s.Config.StorageReserveBytes {
		return model.Fail(503, "storage_capacity_exceeded", "Storage admission limit reached")
	}
	return nil
}
func release(ctx context.Context, tx *sql.Tx, org string, n int) error {
	if _, e := tx.ExecContext(ctx, "UPDATE system_capacity SET active_jobs=active_jobs-$1,updated_at=now() WHERE key='delivery_queue'", n); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "UPDATE organizations SET active_jobs=active_jobs-$2 WHERE id=$1", org, n)
	return e
}
