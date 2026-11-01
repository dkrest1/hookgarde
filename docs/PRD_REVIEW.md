# Hookgarde PRD 2.0 implementation review

Review date: 1 November 2026. Checkout: main at e337b11 before this review's
local documentation/test edits. Authoritative reference: product requirements in
PRD 2.0 (10 October 2026); architecture/security/API chapters support that reference.

**Conclusion: the core self-hosted MVP is implemented and runs, but the project
cannot yet be called fully compliant with all PRD and supporting requirements.**
Implementation, test evidence, operational configuration and release decisions are
separate gates. Existing tests establish useful cases, not proof of every invariant.

## Product scope

| Requirement | Implementation and evidence | Remaining work |
| --- | --- | --- |
| FR01: isolated owner/resources | Operator provisioning, sessions, CSRF/Origin, organizations, projects, endpoints, keys; native PostgreSQL/API tests pass | Expand cross-organization tests across every mutation/history/replay route; independent security review |
| FR02: atomic acceptance/idempotency | Event/jobs/snapshots/receipt/quota/slots share a transaction; concurrent duplicate and forced rollback tests pass on native PostgreSQL | Exercise lost commit responses and real process-restart scenarios |
| FR03: async durable delivery/recovery | Separate API/worker, persistent queue, claim leases, unknown recovery, token fencing; native tests pass | Fix completion error handling described below; broader real multi-worker/crash tests |
| FR04: signing/retry/SSRF | Six attempts, deadline, jitter, Retry-After, HMAC, DNS/IP validation, no redirects/proxy, bounded I/O | Expand rotation/stale-time/timeout/DNS-rebinding tests; configure deployment egress defenses |
| FR05: inspect/pause/replay | Dashboard and API expose event status, history, unknowns, pause and generations; browser fixture workflow passes | Walk through a controlled real failed delivery/replay with the owner; previews deliberately omit arbitrary body text |
| FR06: operable self-hosted core | Both images build; Compose DB/API/worker/web run; migrations and health checks pass; backup/restore smoke passes | Full recovery drill using restored encryption key and controlled delivery; release/security/operations gates |
| FR07: cloud billing | Deferred as specified | Not a missing self-hosted MVP feature |

## Checks actually run in this review

- Built Go API/worker/admin and Next.js Docker images from this checkout.
- Applied migrations explicitly to real PostgreSQL 17.11.
- Started DB/API/worker/web with Compose; API live/ready and dashboard return HTTP 200.
- Ran the Go suite against a separate disposable native PostgreSQL database in Docker:
  all packages passed, including transaction/concurrency and HTTP-to-signed-delivery tests.
  This Docker run used CGO disabled, so it was not a new race-detector run. Earlier
  host race checks are recorded separately in the initial verification record.
- Provisioned the authorized local owner. A real Chromium session signed in through
  the deployed Next.js proxy and displayed the empty dashboard without JS errors.
- Checked desktop and 390px mobile layouts; the empty dashboard had no horizontal
  page overflow. This is not a complete accessibility audit.
- Ran the two repository Playwright scenarios with mocked API fixtures. Corrected
  the login-error test to distinguish the application's error from Next.js's
  built-in route-announcer alert. No application behavior changed for this fix.
- Dumped the local database, restored into a new disposable DB, and checked schema
  version 1 and the owner row. Smoke elapsed time was about 0.45 seconds on this
  tiny dataset. This does not establish production RTO/RPO. Removed test/restore
  databases after checking; the owner's local installation remains running.

## Concrete code gaps

### Completion can hide a database error

`apps/api/internal/store/worker.go`, Store.Complete: the token comparison happens
before checking a general Scan error. When Scan fails before filling token,
`token != c.Token` can cause a nil return. The worker may report a persisted outcome
when it was not persisted. Lease recovery can later recover the job, but error
propagation and operational evidence are wrong.

Required correction: handle sql.ErrNoRows, then all other errors, then compare the
fencing token. Add a regression test that forces a completion lookup failure.
This review records the issue; it does not silently rewrite the implementation.

### Request IDs are absent from audit correlation

The API generates X-Request-ID and the audit schema has request_id, but the audit
insert does not populate it. Supporting security documentation requires identifiers
that connect the API action to audit evidence. Propagate the identifier through an
explicit request context/command field and test a mutation-to-audit correlation.

### Supporting observability requirements are only partially implemented

Basic HTTP duration/counts and queue/outcome gauges exist. Trace propagation and
OpenTelemetry spans/collector configuration are absent. The full documented metric
set (including ingestion-specific latency and detailed worker outcomes) is not
implemented. Therefore the supporting observability design is not complete.

### Storage admission is not free-disk protection

Admission checks PostgreSQL database size against a configured budget minus an
application reserve. It does not measure the database volume's actual remaining
free space. This limitation is documented in ADR 016, but a real deployment still
needs filesystem monitoring/alerts and an enforced operational reserve.

## Nonfunctional and release gates

| PRD target | Current conclusion |
| --- | --- |
| NFR01 persistence | Native transaction/rollback/lease tests pass; broader crash/restart drills pending |
| NFR02 bounded body/time/concurrency | Code and targeted tests present; expanded slow/malicious receiver scenarios pending |
| NFR03 load: 10 events/s, 5 targets, 10k backlog, recorded 2 CPU/4 GiB host | Not benchmarked |
| NFR04 ingestion p95 <300ms / first start p95 <5s under stated conditions | Not measured |
| NFR05 TLS/isolation/tokens/SSRF/redaction | Broad application controls present; production TLS/egress and independent security review pending |
| NFR06 fresh install/migrations/restore | Local cold start and small restore smoke passed; full drill pending |
| NFR07 staging RPO/RTO/availability | Targets only; no scheduled daily backup/monitoring/SLA evidence |
| NFR08 keyboard/labels/text statuses | Implemented elements and browser checks; full keyboard/contrast/accessibility journey pending |

The global capacity lock serializes queue mutations. It is an explicit simplicity
tradeoff, not demonstrated scalability. Benchmark before changing the architecture.

Apache-2.0 was subsequently approved for the current core; see
[ADR 017](adr/017-apache-2-license.md). The future proprietary boundary,
external-launch terms, support ownership and hosting budget remain owner decisions. Public registration/recovery, billing, teams,
Kubernetes, Redis/brokers, strict ordering and exactly-once delivery are deliberately
outside this MVP, not requirements to bolt onto the running local instance.

## Start the learning walkthrough

1. Sign in at http://localhost:3000 with owner@hookgarde.local and the password in
   the ignored `.env.dashboard.local` file. This is a local test account.
2. Create a project and explain how the browser request reaches the Go handler.
3. Add a controlled receiver, save its signing secret, and create an ingestion key.
4. Publish, inspect attempts, cause a failure, recover and replay.
5. Address the completion error regression, then audit correlation/observability.

Keep these steps bounded so the owner can explain the code and its guarantees.
