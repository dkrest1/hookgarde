package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
)

func TestRetryPolicy(t *testing.T) {
	for i, seconds := range []int{10, 40, 160, 640, 2560} {
		if got := Backoff(i+1, 0); got != time.Duration(seconds)*time.Second {
			t.Fatal(got)
		}
		if got := Backoff(i+1, 1); got != time.Duration(seconds)*time.Second*12/10 {
			t.Fatal(got)
		}
	}
	for _, status := range []int{0, 408, 429, 500, 503, 599} {
		if !Retryable(status) {
			t.Fatal(status)
		}
	}
	for _, status := range []int{200, 201, 204, 301, 400, 401, 404, 422} {
		if Retryable(status) {
			t.Fatal(status)
		}
	}
	now := time.Now().UTC()
	if RetryAfter("999999", now) != 6*time.Hour || RetryAfter("-1", now) != 0 || RetryAfter("invalid", now) != 0 || RetryAfter(now.Add(time.Minute).Format(http.TimeFormat), now) > time.Minute {
		t.Fatal("Retry-After policy")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenReader) Close() error             { return nil }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestObserved2xxSurvivesBodyReadFailure(t *testing.T) {
	key := make([]byte, 32)
	cipher, _ := security.Encrypt(key, security.Random(), "o:p:e:k")
	c := model.Claim{OrganizationID: "o", ProjectID: "p", EndpointID: "e", SigningKeyID: "k", Ciphertext: cipher, URL: "http://127.0.0.1", Deadline: time.Now().Add(time.Minute), Sequence: 1, Body: []byte(`{}`)}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.GetBody != nil || r.Header.Get("Webhook-Signature") == "" {
			t.Fatal("replay/signature contract")
		}
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: brokenReader{}}, nil
	})}
	o := Send(context.Background(), c, key, security.Policy{DevelopmentHosts: []string{"127.0.0.1"}}, client)
	if !o.Success || o.ErrorClass != "" {
		t.Fatal(o)
	}
}
func TestNoRedirectAndBoundedPreview(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://127.0.0.1/private")
		w.WriteHeader(302)
		w.Write([]byte(strings.Repeat("sensitive", 5000)))
	}))
	defer receiver.Close()
	key := make([]byte, 32)
	cipher, _ := security.Encrypt(key, security.Random(), "o:p:e:k")
	policy := security.Policy{DevelopmentHosts: []string{"127.0.0.1"}}
	o := Send(context.Background(), model.Claim{OrganizationID: "o", ProjectID: "p", EndpointID: "e", SigningKeyID: "k", Ciphertext: cipher, URL: receiver.URL, Deadline: time.Now().Add(time.Minute), Sequence: 1}, key, policy, policy.Client())
	if o.Status != 302 || o.Retry || o.Success || strings.Contains(o.Preview, "sensitive") || len(o.Preview) > 4096 {
		t.Fatal(o)
	}
}

// The queue interface is a meaningful I/O boundary: these tests exercise actual
// worker scheduling/HTTP while PostgreSQL tests own transactional correctness.
type fakeQueue struct {
	mu        sync.Mutex
	claims    []model.Claim
	completed int
}

func (q *fakeQueue) Claim(ctx context.Context, excluded []string) (*model.Claim, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, c := range q.claims {
		blocked := false
		for _, id := range excluded {
			if c.EndpointID == id {
				blocked = true
			}
		}
		if !blocked {
			q.claims = append(q.claims[:i], q.claims[i+1:]...)
			return &c, nil
		}
	}
	return nil, nil
}
func (q *fakeQueue) Complete(context.Context, model.Claim, model.Outcome) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed++
	return nil
}
func (q *fakeQueue) Maintain(context.Context) error { return nil }
func TestWorkerEndpointCapsAndGracefulDrain(t *testing.T) {
	key := make([]byte, 32)
	q := &fakeQueue{}
	arrived := make(chan struct{}, 12)
	release := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { arrived <- struct{}{}; <-release; w.WriteHeader(204) }))
	defer receiver.Close()
	for i := 0; i < 12; i++ {
		endpoint := fmt.Sprintf("endpoint-%d", i%2)
		cipher, _ := security.Encrypt(key, security.Random(), "o:p:"+endpoint+":k")
		q.claims = append(q.claims, model.Claim{OrganizationID: "o", ProjectID: "p", EndpointID: endpoint, SigningKeyID: "k", Ciphertext: cipher, URL: receiver.URL, Deadline: time.Now().Add(time.Minute), Sequence: 1, Body: []byte(`{}`)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (Worker{Store: q, EncryptionKey: key, Policy: security.Policy{DevelopmentHosts: []string{"127.0.0.1"}}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Run(ctx)
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not start four requests")
		}
	}
	select {
	case <-arrived:
		t.Fatal("per-endpoint cap exceeded")
	case <-time.After(250 * time.Millisecond):
	}
	cancel()
	close(release)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("graceful drain did not finish")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.completed != 4 || len(q.claims) != 8 {
		t.Fatalf("completed=%d remaining=%d", q.completed, len(q.claims))
	}
}
func TestTransientDNSRetries(t *testing.T) {
	key := make([]byte, 32)
	cipher, _ := security.Encrypt(key, security.Random(), "o:p:e:k")
	policy := security.Policy{Lookup: func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("temporary DNS failure") }}
	o := Send(context.Background(), model.Claim{OrganizationID: "o", ProjectID: "p", EndpointID: "e", SigningKeyID: "k", Ciphertext: cipher, URL: "https://receiver.example", Deadline: time.Now().Add(time.Minute), Sequence: 1}, key, policy, policy.Client())
	if !o.Retry || o.ErrorClass != "destination_dns_unavailable" {
		t.Fatal(o)
	}
}
