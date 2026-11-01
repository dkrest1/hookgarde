// Package model defines the wire types shared by the API and delivery worker.
package model

import (
	"encoding/json"
	"time"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string                    { return e.Code }
func Fail(status int, code, message string) error { return &Error{status, code, message} }

type Project struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	Name           string     `json:"name"`
	CreatedAt      time.Time  `json:"created_at"`
	ArchivedAt     *time.Time `json:"archived_at"`
}
type Endpoint struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	URL           string    `json:"url"`
	Description   string    `json:"description"`
	Paused        bool      `json:"paused"`
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	SigningSecret string    `json:"signing_secret,omitempty"`
}
type Key struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"project_id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Scope     string     `json:"scope"`
	Token     string     `json:"token,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}
type EventInput struct {
	Type        string          `json:"type"`
	EndpointIDs []string        `json:"endpoint_ids"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data"`
}
type Envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	ProjectID  string          `json:"project_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	AcceptedAt time.Time       `json:"accepted_at"`
	Data       json.RawMessage `json:"data"`
}
type Acceptance struct {
	EventID     string    `json:"event_id"`
	Status      string    `json:"status"`
	AcceptedAt  time.Time `json:"accepted_at"`
	DeliveryIDs []string  `json:"delivery_ids"`
	Replayed    bool      `json:"idempotency_replayed"`
}
type Event struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	AcceptedAt      time.Time       `json:"accepted_at"`
	Status          string          `json:"status"`
	Envelope        json.RawMessage `json:"envelope,omitempty"`
	PayloadPurgedAt *time.Time      `json:"-"`
}
type Delivery struct {
	ID              string     `json:"id"`
	EventID         string     `json:"event_id"`
	EndpointID      string     `json:"endpoint_id"`
	EndpointVersion int        `json:"endpoint_version"`
	CreatedAt       time.Time  `json:"created_at"`
	Generation      int        `json:"generation"`
	State           string     `json:"state"`
	AttemptsCount   int        `json:"attempts_count"`
	LastError       string     `json:"last_error"`
	DeadlineAt      time.Time  `json:"deadline_at"`
	NextAttemptAt   *time.Time `json:"next_attempt_at"`
	Paused          bool       `json:"-"`
}
type Attempt struct {
	ID              string     `json:"id"`
	Sequence        int        `json:"sequence"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	Outcome         string     `json:"outcome"`
	HTTPStatus      *int       `json:"http_status"`
	DurationMS      *int       `json:"duration_ms"`
	ErrorClass      string     `json:"error_class"`
	ResponsePreview string     `json:"response_preview"`
}
type Principal struct {
	OrganizationID, UserID, SessionID, KeyID, ProjectID string
	CSRFHash                                            []byte
	Email, OrganizationName                             string
}
type Claim struct {
	DeliveryID, OrganizationID, ProjectID, EndpointID, EventID, VersionID, AttemptID, Token, URL, SigningKeyID string
	Sequence, KeyVersion                                                                                       int
	Body, Ciphertext                                                                                           []byte
	Deadline                                                                                                   time.Time
}
type Outcome struct {
	Success, Retry      bool
	Status              int
	DurationMS          int64
	ErrorClass, Preview string
	Delay               time.Duration
}
type List struct {
	Data       any     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}
