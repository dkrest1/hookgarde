package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/config"
	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
	"github.com/dkrest1/hookgarde/apps/api/internal/store"
)

type fixture struct {
	s        *store.Store
	p        model.Principal
	project  model.Project
	endpoint model.Endpoint
	key      model.Key
}

func setup(t *testing.T) fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for real PostgreSQL integration tests")
	}
	c := config.Config{DatabaseURL: dsn, EncryptionKey: make([]byte, 32), Projects: 5, Endpoints: 10, Targets: 10, MonthlyEvents: 10000, OrganizationJobs: 10000, QueueJobs: 100000, StorageLimitBytes: 1 << 40}
	ctx := context.Background()
	s, e := store.Open(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	email := store.ID() + "@example.com"
	if e = s.Provision(ctx, email, "correct-horse-battery-staple", "Test organization"); e != nil {
		t.Fatal(e)
	}
	p, _, e := s.Login(ctx, email, "correct-horse-battery-staple")
	if e != nil {
		t.Fatal(e)
	}
	project, e := s.CreateProject(ctx, p, "Test project")
	if e != nil {
		t.Fatal(e)
	}
	endpoint, e := s.CreateEndpoint(ctx, p, project.ID, "https://example.com/webhook", "Test destination")
	if e != nil {
		t.Fatal(e)
	}
	key, e := s.CreateKey(ctx, p, project.ID, "test publisher", nil)
	if e != nil {
		t.Fatal(e)
	}
	return fixture{s, p, project, endpoint, key}
}
func (f fixture) principal(t *testing.T) model.Principal {
	t.Helper()
	p, e := f.s.Bearer(context.Background(), f.key.Token, f.project.ID)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func (f fixture) input() (model.EventInput, []byte) {
	input := model.EventInput{Type: "order.paid", EndpointIDs: []string{f.endpoint.ID}, OccurredAt: time.Now().UTC(), Data: json.RawMessage(`{"order_id":"123"}`)}
	body, _ := json.Marshal(input)
	return input, body
}
func accept(t *testing.T, f fixture, key string) model.Acceptance {
	t.Helper()
	input, body := f.input()
	a, e := f.s.Accept(context.Background(), f.principal(t), key, body, input)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func count(t *testing.T, f fixture, table string) int {
	t.Helper()
	var n int
	if e := f.s.DB.QueryRow("SELECT count(*) FROM "+table+" WHERE organization_id=$1", f.p.OrganizationID).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func status(err error) int {
	if e, ok := err.(*model.Error); ok {
		return e.Status
	}
	return 0
}
func TestConcurrentAcceptanceAndAtomicRollback(t *testing.T) {
	f := setup(t)
	p := f.principal(t)
	input, body := f.input()
	var wg sync.WaitGroup
	receipts := make(chan model.Acceptance, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, e := f.s.Accept(context.Background(), p, "same-key", body, input)
			receipts <- a
			errs <- e
		}()
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for a := range receipts {
		if id != "" && id != a.EventID {
			t.Fatal("duplicate event accepted")
		}
		id = a.EventID
	}
	if count(t, f, "events") != 1 || count(t, f, "deliveries") != 1 {
		t.Fatal("duplicate rows")
	}
	var usage int
	if e := f.s.DB.QueryRow("SELECT accepted_event_count FROM usage_months WHERE organization_id=$1", p.OrganizationID).Scan(&usage); e != nil || usage != 1 {
		t.Fatal(usage, e)
	}
	_, e := f.s.Accept(context.Background(), p, "same-key", append(body, ' '), input)
	if status(e) != 409 {
		t.Fatal("different bytes did not conflict", e)
	}
	input.EndpointIDs = append(input.EndpointIDs, store.ID())
	if _, e = f.s.Accept(context.Background(), p, "invalid-target", body, input); status(e) != 404 {
		t.Fatal(e)
	}
	if count(t, f, "events") != 1 {
		t.Fatal("partial event accepted")
	}
	// Force a database constraint failure after queue/quota reservation. The entire
	// transaction, including counters, must roll back.
	bad, raw := f.input()
	bad.Type = ""
	_, e = f.s.Accept(context.Background(), p, "forced-rollback", raw, bad)
	if e == nil {
		t.Fatal("empty event type accepted by DB")
	}
	if count(t, f, "events") != 1 {
		t.Fatal("failed transaction committed")
	}
	var active int
	if e = f.s.DB.QueryRow("SELECT active_jobs FROM organizations WHERE id=$1", p.OrganizationID).Scan(&active); e != nil || active != 1 {
		t.Fatal(active, e)
	}
}
func TestQuotaPauseScopeAndKeyRevocation(t *testing.T) {
	f := setup(t)
	p := f.principal(t)
	input, body := f.input()
	f.s.Config.MonthlyEvents = 1
	a, e := f.s.Accept(context.Background(), p, "first", body, input)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.s.Accept(context.Background(), p, "first", body, input)
	if e != nil || !again.Replayed || again.EventID != a.EventID {
		t.Fatal(again, e)
	}
	if _, e = f.s.Accept(context.Background(), p, "second", body, input); status(e) != 429 {
		t.Fatal(e)
	}
	if count(t, f, "events") != 1 {
		t.Fatal("quota rejection inserted event")
	}
	paused := true
	if _, e = f.s.UpdateEndpoint(context.Background(), f.p, f.project.ID, f.endpoint.ID, nil, nil, &paused); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Accept(context.Background(), p, "paused", body, input); status(e) != 409 {
		t.Fatal(e)
	}
	other := setup(t)
	if _, e = f.s.Project(context.Background(), other.p.OrganizationID, f.project.ID); status(e) != 404 {
		t.Fatal(e)
	}
	if _, e = f.s.Bearer(context.Background(), f.key.Token, other.project.ID); status(e) != 404 {
		t.Fatal(e)
	}
	if e = f.s.RevokeKey(context.Background(), f.p, f.project.ID, f.key.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Accept(context.Background(), p, "first", body, input); status(e) != 401 {
		t.Fatal("revoked principal was trusted", e)
	}
}
func TestRecoveryFencesStaleCompletionAndReplay(t *testing.T) {
	f := setup(t)
	a := accept(t, f, "recover")
	ctx := context.Background()
	c := claimSpecific(t, f, a.DeliveryIDs[0])
	if _, e := f.s.DB.Exec("UPDATE deliveries SET lease_until=now()-interval '1 second' WHERE id=$1", c.DeliveryID); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Maintain(ctx); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Complete(ctx, *c, model.Outcome{Success: true, Status: 200}); e != nil {
		t.Fatal(e)
	}
	var state, outcome string
	if e := f.s.DB.QueryRow("SELECT state FROM deliveries WHERE id=$1", c.DeliveryID).Scan(&state); e != nil || state != "retry_wait" {
		t.Fatal(state, e)
	}
	if e := f.s.DB.QueryRow("SELECT outcome FROM delivery_attempts WHERE id=$1", c.AttemptID).Scan(&outcome); e != nil || outcome != "unknown" {
		t.Fatal(outcome, e)
	}
	if _, e := f.s.DB.Exec("UPDATE deliveries SET next_attempt_at=now() WHERE id=$1", c.DeliveryID); e != nil {
		t.Fatal(e)
	}
	c = claimSpecific(t, f, c.DeliveryID)
	if c.Sequence != 2 {
		t.Fatal(c.Sequence)
	}
	if e := f.s.Complete(ctx, *c, model.Outcome{Success: true, Status: 204}); e != nil {
		t.Fatal(e)
	}
	replay, e := f.s.Replay(ctx, f.p, f.project.ID, c.DeliveryID, "replay-key", []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.s.Replay(ctx, f.p, f.project.ID, c.DeliveryID, "replay-key", []byte(`{}`))
	if e != nil || replay["delivery_id"] != again["delivery_id"] || again["idempotency_replayed"] != true {
		t.Fatal(again, e)
	}
	if _, e = f.s.Replay(ctx, f.p, f.project.ID, c.DeliveryID, "different", []byte(`{}`)); status(e) != 409 {
		t.Fatal(e)
	}
	var oldVersion, newVersion string
	if e = f.s.DB.QueryRow("SELECT endpoint_version_id FROM deliveries WHERE id=$1", c.DeliveryID).Scan(&oldVersion); e != nil {
		t.Fatal(e)
	}
	if e = f.s.DB.QueryRow("SELECT endpoint_version_id FROM deliveries WHERE id=$1", replay["delivery_id"]).Scan(&newVersion); e != nil || newVersion != oldVersion {
		t.Fatal(newVersion, e)
	}
}
func claimSpecific(t *testing.T, f fixture, id string) *model.Claim {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		c, e := f.s.Claim(ctx, []string{})
		if e != nil {
			t.Fatal(e)
		}
		if c == nil {
			t.Fatal("expected pending claim")
		}
		if c.DeliveryID == id {
			return c
		}
		if e = f.s.Complete(ctx, *c, model.Outcome{Success: true, Status: 200}); e != nil {
			t.Fatal(e)
		}
	}
	t.Fatal("claim not found")
	return nil
}
func TestFinalAttemptCrashAndPausedDeadline(t *testing.T) {
	f := setup(t)
	a := accept(t, f, "exhausted")
	c := claimSpecific(t, f, a.DeliveryIDs[0])
	if _, e := f.s.DB.Exec("UPDATE deliveries SET attempts_count=6,lease_until=now()-interval '1 second' WHERE id=$1", c.DeliveryID); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Maintain(context.Background()); e != nil {
		t.Fatal(e)
	}
	var state string
	if e := f.s.DB.QueryRow("SELECT state FROM deliveries WHERE id=$1", c.DeliveryID).Scan(&state); e != nil || state != "failed" {
		t.Fatal(state, e)
	}
	a = accept(t, f, "paused-expiry")
	paused := true
	if _, e := f.s.UpdateEndpoint(context.Background(), f.p, f.project.ID, f.endpoint.ID, nil, nil, &paused); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.DB.Exec("UPDATE deliveries SET deadline_at=now()-interval '1 second' WHERE id=$1", a.DeliveryIDs[0]); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Maintain(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := f.s.DB.QueryRow("SELECT state FROM deliveries WHERE id=$1", a.DeliveryIDs[0]).Scan(&state); e != nil || state != "failed" {
		t.Fatal(state, e)
	}
}
func TestRetentionArchiveAndCrossTenantForeignKey(t *testing.T) {
	f := setup(t)
	a := accept(t, f, "retention")
	c := claimSpecific(t, f, a.DeliveryIDs[0])
	if e := f.s.Complete(context.Background(), *c, model.Outcome{Success: true, Status: 200}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.DB.Exec("UPDATE events SET retention_at=now()-interval '1 second' WHERE id=$1", a.EventID); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Maintain(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Replay(context.Background(), f.p, f.project.ID, c.DeliveryID, "expired", []byte(`{}`)); status(e) != 410 {
		t.Fatal(e)
	}
	other := setup(t)
	_, e := f.s.DB.Exec("INSERT INTO api_keys(id,organization_id,project_id,name,prefix,token_hash) VALUES($1,$2,$3,'invalid','invalid',$4)", store.ID(), other.p.OrganizationID, f.project.ID, security.Hash("invalid"))
	if e == nil {
		t.Fatal("cross-tenant foreign key accepted")
	}
	a = accept(t, f, "archive")
	archive := true
	if _, e = f.s.UpdateProject(context.Background(), f.p, f.project.ID, nil, &archive); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = f.s.DB.QueryRow("SELECT state FROM deliveries WHERE id=$1", a.DeliveryIDs[0]).Scan(&state); e != nil || state != "canceled" {
		t.Fatal(state, e)
	}
	if e = f.s.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestQueueCapacityAndImmutableURL(t *testing.T) {
	f := setup(t)
	f.s.Config.OrganizationJobs = 1
	a := accept(t, f, "capacity")
	input, body := f.input()
	if _, e := f.s.Accept(context.Background(), f.principal(t), "capacity-two", body, input); status(e) != 503 {
		t.Fatal(e)
	}
	url := "https://example.com/new"
	if _, e := f.s.UpdateEndpoint(context.Background(), f.p, f.project.ID, f.endpoint.ID, &url, nil, nil); e != nil {
		t.Fatal(e)
	}
	c := claimSpecific(t, f, a.DeliveryIDs[0])
	if c.URL == url {
		t.Fatal("accepted URL changed")
	}
	if e := f.s.Complete(context.Background(), *c, model.Outcome{Success: true, Status: 200}); e != nil {
		t.Fatal(e)
	}
	accept(t, f, "capacity-freed")
	var active int
	if e := f.s.DB.QueryRow("SELECT active_jobs FROM organizations WHERE id=$1", f.p.OrganizationID).Scan(&active); e != nil || active != 1 {
		t.Fatal(fmt.Sprint(active), e)
	}
}
