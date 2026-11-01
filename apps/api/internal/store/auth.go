package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
)

func (s *Store) Provision(ctx context.Context, email, password, name string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	a, e := mail.ParseAddress(email)
	if e != nil || a.Address != email || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 || len(strings.TrimSpace(name)) < 1 || utf8.RuneCountInString(name) > 120 || len(email) > 254 {
		return fmt.Errorf("provide a valid email, 12-128 character password and 1-120 character organization name")
	}
	hash := security.PasswordHash(password)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		org, user := ID(), ID()
		if _, e := tx.ExecContext(ctx, "INSERT INTO organizations(id,name) VALUES($1,$2)", org, name); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3)", user, email, hash); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,$2,'owner')", org, user); e != nil {
			return e
		}
		return audit(ctx, tx, model.Principal{OrganizationID: org, UserID: user}, "owner.provisioned", "organization", org)
	})
}
func (s *Store) Login(ctx context.Context, email, password string) (model.Principal, string, error) {
	var p model.Principal
	var hash string
	e := s.DB.QueryRowContext(ctx, `SELECT u.id,u.email,u.password_hash,o.id,o.name FROM users u JOIN organization_memberships m ON m.user_id=u.id JOIN organizations o ON o.id=m.organization_id WHERE u.email=$1 AND u.disabled_at IS NULL`, strings.ToLower(strings.TrimSpace(email))).Scan(&p.UserID, &p.Email, &hash, &p.OrganizationID, &p.OrganizationName)
	if e != nil && e != sql.ErrNoRows {
		return p, "", e
	}
	if e == sql.ErrNoRows {
		// A fixed well-formed dummy hash makes an unknown account perform one
		// Argon2id verification, matching the cost of a wrong real password.
		hash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	valid := security.VerifyPassword(password, hash)
	if !valid || e == sql.ErrNoRows {
		return p, "", model.Fail(401, "invalid_credentials", "Invalid email or password")
	}
	token := security.Random()
	p.SessionID = ID()
	csrf := security.CSRF(string(s.Config.EncryptionKey), token)
	p.CSRFHash = security.Hash(csrf)
	_, e = s.DB.ExecContext(ctx, `INSERT INTO sessions(id,organization_id,user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '24 hours')`, p.SessionID, p.OrganizationID, p.UserID, security.Hash(token), p.CSRFHash)
	return p, token, e
}
func (s *Store) Session(ctx context.Context, token string) (model.Principal, error) {
	var p model.Principal
	e := s.DB.QueryRowContext(ctx, `SELECT s.id,s.organization_id,s.user_id,s.csrf_hash,u.email,o.name FROM sessions s JOIN users u ON u.id=s.user_id JOIN organizations o ON o.id=s.organization_id JOIN organization_memberships m ON m.organization_id=s.organization_id AND m.user_id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.disabled_at IS NULL`, security.Hash(token)).Scan(&p.SessionID, &p.OrganizationID, &p.UserID, &p.CSRFHash, &p.Email, &p.OrganizationName)
	if e == sql.ErrNoRows {
		return p, model.Fail(401, "unauthorized", "Login required")
	}
	return p, e
}
func (s *Store) Bearer(ctx context.Context, token, project string) (model.Principal, error) {
	var p model.Principal
	e := s.DB.QueryRowContext(ctx, `SELECT k.id,k.organization_id,k.project_id FROM api_keys k WHERE k.token_hash=$1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now())`, security.Hash(token)).Scan(&p.KeyID, &p.OrganizationID, &p.ProjectID)
	if e == sql.ErrNoRows {
		return p, model.Fail(401, "unauthorized", "Invalid ingestion key")
	}
	if e != nil {
		return p, e
	}
	if p.ProjectID != project {
		return p, missing()
	}
	return p, nil
}
func (s *Store) Logout(ctx context.Context, p model.Principal) error {
	_, e := s.DB.ExecContext(ctx, "UPDATE sessions SET revoked_at=now() WHERE id=$1 AND organization_id=$2", p.SessionID, p.OrganizationID)
	return e
}
func (s *Store) Rate(ctx context.Context, scope, action string, limit int, window time.Duration) error {
	mac := hmac.New(sha256.New, s.Config.EncryptionKey)
	mac.Write([]byte(scope))
	bucket := time.Now().UTC().Truncate(window)
	var count int
	e := s.DB.QueryRowContext(ctx, `INSERT INTO rate_limit_buckets(scope_hash,action,window_start,count,expires_at) VALUES($1,$2,$3,1,$4) ON CONFLICT(scope_hash,action,window_start) DO UPDATE SET count=rate_limit_buckets.count+1 WHERE rate_limit_buckets.count<$5 RETURNING count`, mac.Sum(nil), action, bucket, bucket.Add(2*window), limit).Scan(&count)
	if e == sql.ErrNoRows {
		return model.Fail(429, "rate_limited", "Rate limit exceeded")
	}
	return e
}
