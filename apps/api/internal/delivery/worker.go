// Package delivery sends signed requests outside database transactions.
package delivery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
	"github.com/dkrest1/hookgarde/apps/api/internal/security"
)

func Retryable(status int) bool {
	return status == 0 || status == 408 || status == 429 || status >= 500
}
func Backoff(sequence int, jitter float64) time.Duration {
	if sequence < 1 {
		sequence = 1
	}
	base := 10 * time.Second
	for i := 1; i < sequence; i++ {
		base *= 4
		if base >= 2*time.Hour {
			base = 2 * time.Hour
			break
		}
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	return base + time.Duration(float64(base)*0.2*jitter)
}
func RetryAfter(value string, now time.Time) time.Duration {
	var d time.Duration
	if n, e := strconv.ParseInt(value, 10, 32); e == nil {
		d = time.Duration(n) * time.Second
	} else if t, e := http.ParseTime(value); e == nil {
		d = t.Sub(now)
	}
	if d < 0 {
		return 0
	}
	if d > 6*time.Hour {
		return 6 * time.Hour
	}
	return d
}
func randomFraction() float64 {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return 0
	}
	return float64(binary.BigEndian.Uint64(b)>>11) / (1 << 53)
}
func Preview(body []byte, secrets ...string) string {
	v := strings.ToValidUTF8(string(body), "�")
	for _, secret := range secrets {
		if secret != "" {
			v = strings.ReplaceAll(v, secret, "[redacted]")
		}
	}
	v = strings.Map(func(r rune) rune {
		if r == 0 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, v)
	if len(v) > 4096 {
		v = v[:4096]
		v = strings.ToValidUTF8(v, "")
	}
	return v
}

// Receiver bodies can echo arbitrary credentials or event payloads. Store only
// an allowlisted transport summary instead of persisting potentially secret text.
func SafeResponsePreview(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return "[receiver response body omitted; " + strconv.Itoa(len(body)) + " bytes sampled]"
}
func Send(ctx context.Context, c model.Claim, key []byte, policy security.Policy, client *http.Client) model.Outcome {
	started := time.Now()
	deadline := started.Add(10 * time.Second)
	if c.Deadline.Before(deadline) {
		deadline = c.Deadline
	}
	requestCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	o := model.Outcome{Retry: true, ErrorClass: "transport_error", Delay: Backoff(c.Sequence, randomFraction())}
	secret, e := security.Decrypt(key, c.Ciphertext, c.OrganizationID+":"+c.ProjectID+":"+c.EndpointID+":"+c.SigningKeyID)
	if e != nil {
		o.Retry = false
		o.ErrorClass = "signing_key_unavailable"
		return o
	}
	if e = policy.Validate(requestCtx, c.URL); e != nil {
		o.Retry = errors.Is(e, security.ErrDNSUnavailable) || requestCtx.Err() != nil
		o.ErrorClass = "destination_policy_rejected"
		if o.Retry {
			o.ErrorClass = "destination_dns_unavailable"
		}
		return o
	}
	req, e := http.NewRequestWithContext(requestCtx, http.MethodPost, c.URL, bytes.NewBuffer(c.Body))
	if e != nil {
		o.Retry = false
		o.ErrorClass = "invalid_destination"
		return o
	}
	req.GetBody = nil // prevent transparent replay of a POST outside the tracked attempt.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Webhook-Event-Id", c.EventID)
	req.Header.Set("Webhook-Delivery-Id", c.DeliveryID)
	req.Header.Set("Webhook-Attempt-Id", c.AttemptID)
	signature, e := security.Signature(secret, time.Now().Unix(), c.KeyVersion, c.Body)
	if e != nil {
		o.Retry = false
		o.ErrorClass = "signing_key_unavailable"
		return o
	}
	req.Header.Set("Webhook-Signature", signature)
	resp, e := client.Do(req)
	o.DurationMS = time.Since(started).Milliseconds()
	if e != nil {
		return o
	}
	defer resp.Body.Close()
	o.Status = resp.StatusCode
	o.Success = o.Status >= 200 && o.Status < 300
	o.Retry = Retryable(o.Status)
	o.ErrorClass = "receiver_http_" + strconv.Itoa(o.Status)
	if o.Success {
		o.ErrorClass = ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	o.Preview = SafeResponsePreview(body)
	if o.Status == 429 || o.Status == 503 {
		if d := RetryAfter(resp.Header.Get("Retry-After"), time.Now()); d > o.Delay {
			o.Delay = d
		}
	}
	o.DurationMS = time.Since(started).Milliseconds()
	return o
}

type Queue interface {
	Claim(context.Context, []string) (*model.Claim, error)
	Complete(context.Context, model.Claim, model.Outcome) error
	Maintain(context.Context) error
}
type Worker struct {
	Store         Queue
	EncryptionKey []byte
	Policy        security.Policy
	Log           *slog.Logger
}

func (w Worker) Run(ctx context.Context) error {
	client := w.Policy.Client()
	sendCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	active := map[string]int{}
	var mu sync.Mutex
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Second)
	defer maintenance.Stop()
	drain := func() {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			cancel()
			<-done
		}
	}
	for {
		select {
		case <-ctx.Done():
			drain()
			return nil
		case <-maintenance.C:
			maintenanceCtx, stop := context.WithTimeout(ctx, 12*time.Second)
			if e := w.Store.Maintain(maintenanceCtx); e != nil && ctx.Err() == nil {
				w.Log.Error("worker maintenance failed")
			}
			stop()
		case <-ticker.C:
			for i := 0; i < 10; i++ {
				mu.Lock()
				total := 0
				excluded := []string{}
				for id, n := range active {
					total += n
					if n >= 2 {
						excluded = append(excluded, id)
					}
				}
				mu.Unlock()
				if total >= 10 {
					break
				}
				claimCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				c, e := w.Store.Claim(claimCtx, excluded)
				stop()
				if e != nil {
					if ctx.Err() == nil {
						w.Log.Error("delivery claim failed")
					}
					break
				}
				if c == nil {
					break
				}
				mu.Lock()
				active[c.EndpointID]++
				mu.Unlock()
				wg.Add(1)
				go func(c model.Claim) {
					defer wg.Done()
					defer func() { mu.Lock(); active[c.EndpointID]--; mu.Unlock() }()
					o := Send(sendCtx, c, w.EncryptionKey, w.Policy, client)
					completeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if e := w.Store.Complete(completeCtx, c, o); e != nil {
						w.Log.Error("delivery outcome persistence failed", "delivery_id", c.DeliveryID)
					} else {
						w.Log.Info("delivery attempted", "delivery_id", c.DeliveryID, "attempt_id", c.AttemptID, "http_status", o.Status, "error_class", o.ErrorClass)
					}
				}(*c)
			}
		}
	}
}
