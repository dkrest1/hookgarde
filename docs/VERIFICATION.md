# Verification record

Implementation date: 1 November 2026. Baseline repository main was
`70f094b4262e9abf9ca1a109b2b68ef4a01cb9c5`. Changes are in the local checkout;
no GitHub publication or deployment is implied by this record.

## Implemented scope

- Go net/http API with strict GET liveness, DB/schema readiness, configurable bind,
  HTTP timeouts and graceful shutdown.
- Operator provisioning, Argon2id credentials, opaque sessions, CSRF/Origin checks,
  organization isolation, project lifecycle, endpoint URL versions/pause/rotation,
  hashed project ingestion keys and revocation.
- PostgreSQL migrations and constraints, atomic event acceptance, byte-exact
  idempotency, UTC quota, shared capacity, storage admission and rate buckets.
- Independently runnable worker, encrypted versioned signing keys, SSRF-safe
  connection policy, bounded signed delivery, retries, deadline/lease recovery,
  fenced completion, replay, retention and counter reconciliation.
- Next.js/TypeScript console: login, setup, one-time disclosure, endpoints/keys,
  publishing tool, filtered cursor history, attempts and replay, project archive.
- Private basic metrics, structured logs, audit evidence, Docker Compose/images,
  pinned CI, configuration/runbooks and reading guide.

## What verification means

Go unit tests exercise protocol behavior, strict JSON, encryption scope, password
verification, exact-byte signatures, SSRF addresses/mixed DNS, response handling,
retry policy, configuration and graceful drain.

SQL integration tests exercise migrations, concurrent duplicate acceptance,
rollback, quota and queue admission, key revocation, tenant constraints, pause,
immutable URL versions, replay, retention, deadline exhaustion and stale-worker
fencing. The HTTP integration journey covers session/CSRF, resource setup,
publication/dedupe, signed delivery and history. Responses are checked against the
supplied OpenAPI schemas.

Local SQL tests ran using **PGlite's embedded PostgreSQL engine with its socket
adapter**, with pgx simple protocol. This validates SQL and application flow, but
is **not evidence of native multi-session PostgreSQL locking correctness**. Native
PostgreSQL startup was blocked by the desktop sandbox's shared-memory restriction.
CI explicitly runs the same integration suite against PostgreSQL 17.

Frontend type checking, DOM interaction tests and the production build run locally.
DOM tests mock the API, so they do not prove browser layout or end-to-end persistence.
The Playwright suite is present, but local Chromium startup was blocked by the
sandbox's macOS Mach-port permission. CI runs Chromium separately.

Compose configuration parses locally. Docker images, cold start and restore smoke
cannot run locally because neither configured Docker daemon socket exists. CI
builds both images and performs installation/health/backup restore checks.

The initial Go vulnerability scan found affected Go 1.27.1 standard-library paths.
The module minimum and container/CI toolchains were moved to Go 1.27.3; final scan
scan reports no reachable vulnerabilities and no vulnerabilities in imported
packages. One module-level advisory (GO-2026-5932) concerns the unused, unmaintained
`golang.org/x/crypto/openpgp` package; Hookgarde imports Argon2id, not OpenPGP. The npm audit
reports zero vulnerabilities. JavaScript dependencies are lockfile pinned.

Additional local checks passed: `go vet`, Go command builds, the race-enabled Go
suite, frontend type checking, two DOM interaction tests, the Next.js production
build, OpenAPI validation/response checks, and Compose configuration parsing.

## Still required before a production release

- Pass native PostgreSQL multi-session/concurrency suite and real browser checks.
- Build images, run Compose cold start and a controlled signed delivery workflow.
- Complete a real backup/restore drill with the separately backed-up encryption key.
- Measure the PRD load/latency targets on a recorded host; no benchmark or SLA claimed.
- Review SSRF/tenant/redaction controls independently and configure egress defenses.
- Resolve license, commercial boundary, support, hosting and external-launch policies.
- Add a reviewed encryption-master-key re-encryption procedure before rotating it.

Full distributed tracing, public signup/recovery, teams, billing, Kubernetes and
managed cloud remain outside this alpha's current operational evidence. Basic
metrics are implemented; no deployed collector or production observability stack
is claimed.
