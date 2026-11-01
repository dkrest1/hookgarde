# Read, run, break, improve

The implementation workflow changed at the owner's request: the assistant writes
the complete self-hosted code; the owner reads, tests, modifies and explains it.
The product scope and reliability contracts still come from PRD 2.0.

Start by making one delivery work using the README. Then read in this order.

## 1. Follow an HTTP request

1. `apps/api/cmd/api/main.go`: configuration, signal context, DB connection, schema
   readiness, dependency construction and HTTP server startup.
2. `apps/api/internal/server/server.go`: the listener and HTTP timeouts; stopping
   the process drains active requests within a bounded shutdown period.
3. `apps/api/internal/httpapi/router.go`: route selection, authentication, Origin,
   CSRF, request ID and rate policy. Unknown paths return 404; unsupported methods
   return 405 with Allow. Health liveness is deliberately independent of PostgreSQL.
4. `apps/api/internal/httpapi/json.go`: body size, media type, duplicate properties,
   exact top-level property names, depth and trailing-data checks.
5. `apps/api/internal/httpapi/resources.go`: wire validation, calls into storage,
   and success responses. These handlers never perform outbound delivery.

`http.Handler` lets both a real server and `httptest` invoke the same routing and
handler logic. `http.ResponseWriter` commits headers when status/body is first
written. `*http.Request` carries method, URL, headers, body and cancelable context.
A context bounds I/O; it is not a place to put mutable global application state.

Exercise: add a health response field, update its contract/test, and explain why a
POST must still fail. Try a duplicate JSON property and inspect the error response.

## 2. Follow a publication transaction

Read `store.Accept` in `internal/store/ingestion.go` alongside migration 001.

- The authenticated key determines organization/project scope.
- Shared capacity and organization locks serialize admission policy changes.
- A retained idempotency record is checked before project/quota/capacity rejection.
- Endpoint URL versions are captured; any invalid or paused target rejects all.
- Capacity, monthly usage, event bytes, deliveries and receipt commit together.
- The response is sent only after `Commit` succeeds.

A transaction's deferred `Rollback` releases resources on every early return. After
a successful `Commit`, the deferred rollback does no further work. Returning a DB
error makes HTTP return 503; the producer retries with the same key/body to resolve
an uncertain commit. A Go mutex would not protect these invariants across replicas.

The schema's composite foreign keys enforce tenant/project relationships even if
a future application query accidentally omits a check. SQL parameters protect
values; dynamic SQL identifiers appear only in fixed internal/test code.

Exercise: run the concurrent acceptance test. Change whitespace under the same key.
Force a transaction failure and verify event count, monthly usage and slots remain
unchanged. Explain why “SELECT quota; INSERT later” would race without a lock.

## 3. Follow a delivery attempt

1. `store.Claim` selects an eligible job fairly by organization, reserves an attempt
   and commits a 60-second lease/token. Paused endpoints cannot acquire new claims.
2. `delivery.Worker.Run` caps concurrency; network calls run outside DB transactions.
3. `delivery.Send` decrypts the current signing key, validates the URL/DNS, signs
   exact envelope bytes and makes one bounded HTTP POST with no redirects/proxy.
4. `store.Complete` checks the lease token before writing outcome/next state.
5. `store.Maintain` marks crashed attempts unknown, fences stale workers and
   schedules bounded retry or visible terminal exhaustion.

The lease prevents concurrent database ownership, not duplicate remote business
side effects. A receiver may accept a request immediately before the worker dies.
The next worker cannot know whether that happened. This is why receiver dedupe is
required and why an attempt outcome can be unknown.

Exercise: stop a worker after claim, then restart it after lease expiry. Explain
why the old token must not change the recovered attempt to success. Compare a
302, 404, 429, 503, timeout and 204 response. Trace the final-attempt crash case.

## 4. Follow replay and retention

Replay creates a fresh generation with the same event bytes and original URL
version. It resets only budget/deadline. A partial unique index prevents competing
active generations; replay receipts recover uncertain API responses. A paused or
archived destination cannot be replayed, and a purged body returns 410.

Bodies are eligible for purge seven days after all generations become terminal.
Active work keeps its body regardless of age. Dedupe receipts retain metadata for
90 days. Purging live data does not erase historical backups.

Exercise: edit an endpoint URL after publication. Confirm the accepted delivery and
its replay still use the old URL version. Write down when a producer may reuse a
previously expired idempotency key.

## 5. Inspect credentials and isolation

Read `security/secrets.go`, `security/outbound.go`, `store/auth.go` and
`store/resources.go`. Passwords use Argon2id; random session/key tokens are hashed.
Signing secrets use AES-GCM with scope-bound associated data. The master key stays
outside PostgreSQL and must be backed up separately. Browser mutations require
both an exact Origin and the session's CSRF token. A project ingestion key grants
no dashboard access.

Exercise: provision a second organization and request the first organization's
project, event, endpoint and attempts. Confirm no foreign data leaks. Test DNS that
returns one public and one private address. Explain why resolving a hostname once
and letting the HTTP transport resolve it again would be unsafe.

## 6. Read the dashboard and deployment

- `apps/web/app/page.tsx` orchestrates the owner console and its API interactions.
- `app/api/[...path]/route.ts` proxies only `/v1` to the configured Go API; it bounds
  request bodies and forwards cookies/Origin/CSRF rather than granting privileges.
- `app/globals.css` supplies responsive layout and visible focus styles.
- DOM tests cover interactions; Playwright covers browser layout and the wider flow.
- Compose builds independent API/worker processes using one Go module and one DB.
- CI tests native PostgreSQL, browser behavior, images and a backup restore smoke.

Exercise: change the retry history presentation without changing backend behavior.
Then add a useful new HTTP regression test, break the implementation deliberately,
and prove the test fails before repairing it. Refactoring each console tab into a
component is a useful frontend improvement once you can explain the current flow.

## Reading checklist for any change

What invariant does this code protect? What is in memory versus durable storage?
Which failure leaves an uncertain result? Which tests would catch my change being
wrong? How does the same code behave with two processes? Explain your answers in
your own words before relying on an implementation detail.
