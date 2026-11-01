# Hookgarde

Reliable webhooks. Zero infrastructure headaches.

Hookgarde is a self-hosted outbound webhook delivery application: a Go HTTP API,
PostgreSQL-backed delivery worker, and Next.js operator console. Publish an event,
inspect each attempt, and replay a terminal delivery without losing its history.
The managed-service tagline is an aspiration; self-hosted operators own backups,
monitoring and infrastructure.

This is an **alpha implementation for review and learning**, not a production
readiness certification. See [verification status](docs/VERIFICATION.md) for the
checks actually run and outstanding environment-dependent validation.

## License

Hookgarde is licensed under the [Apache License 2.0](LICENSE).
Copyright 2026 Oluwatosin Temitope Akande. See [NOTICE](NOTICE) for attribution.
Third-party dependencies retain their respective licenses.

The current repository is the Apache-licensed self-hosted core. A future proprietary
feature boundary remains undecided; no additional commercial restrictions apply
to this code. See [the licensing decision](docs/adr/017-apache-2-license.md).

## Start locally with Docker Compose

Requirements: Docker Engine with Compose v2, Python 3 for the optional configuration
helper. Images, Go modules, npm dependencies and CI actions are pinned.

From the repository root:

```sh
python3 scripts/create-local-env.py
docker compose build
docker compose up -d db
docker compose run --rm migrate
docker compose up -d --wait api worker web
```

The helper creates `.env` once with fresh secrets and mode `0600`. It never prints
secrets or overwrites an existing file. Review `.env` before starting. Do not commit it.
Migrations are an explicit step; API/worker refuse an incompatible schema.

Provision an owner (no public signup). Read the password without putting it into
shell history; this example is for zsh:

```sh
read -rs 'OWNER_PASSWORD?New owner password (12-128 characters): '
export OWNER_PASSWORD
docker compose run --rm -e OWNER_PASSWORD admin provision \
  --email you@example.com --organization 'My workspace'
unset OWNER_PASSWORD
```

Open [the console](http://localhost:3000), sign in, create a project, add an HTTPS
endpoint, save its signing secret, and create an ingestion key. The console's
Publish event form uses that ingestion key; owner sessions cannot publish events.

```sh
curl -i http://localhost:8080/health/live
```

The API and console bind only to localhost on the host. PostgreSQL is private to
the Compose network. `COOKIE_SECURE=false` is restricted to a localhost public
origin. Before a non-local deployment configure HTTPS, `PUBLIC_ORIGIN`,
`COOKIE_SECURE=true`, secrets, backup storage and network restrictions.

### Local receiver exercise

For an explicit local development receiver, edit `.env`:

```dotenv
APP_ENV=development
DEV_ENDPOINT_HOSTS=receiver
RECEIVER_STATUS=204
```

```sh
docker compose --profile demo up -d --force-recreate api worker receiver
```

Create an endpoint `http://receiver:8090/webhook`. The exact development allowlist
is the only private-address exception, and it is rejected outside development mode.
The demo receiver discards bodies and returns 204; it does **not** verify signatures.
For failures, set `RECEIVER_STATUS=503` and recreate `receiver`. Publish, inspect
retry evidence, restore 204 and wait for retry. To test replay, use a terminal
failure (e.g. exhaust its finite attempt budget), then replay after recovery.

## Run without containers

Use Go 1.27.3 or later and Node 24.16.0 or later in the Node 24 line, with a
separately running PostgreSQL 17 database. Container/CI patch versions are pinned
independently. Export `DATABASE_URL`, `ENCRYPTION_KEY`, `PUBLIC_ORIGIN` and
`COOKIE_SECURE` from your environment; `.env` is **not automatically loaded by Go**.
A shell should export explicit values rather than execute an untrusted env file.

From `apps/api`:

```sh
go run ./cmd/admin migrate
go run ./cmd/admin provision --email you@example.com --organization 'My workspace'
go run ./cmd/api
```

Run `go run ./cmd/worker` in another terminal. `OWNER_PASSWORD` must be present only
for provisioning. Run `npm ci && npm run dev` from `apps/web` in a third terminal.
`API_INTERNAL_URL` defaults to `http://127.0.0.1:8080` for the Next.js API proxy.

## Configuration

| Variable | Default / requirement |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL DSN; use TLS for remote PostgreSQL |
| `ENCRYPTION_KEY` | Required base64-encoded 32 bytes; keep for DB restore |
| `HTTP_ADDR` | `127.0.0.1:8080`; Compose uses its internal `0.0.0.0:8080` |
| `PUBLIC_ORIGIN` | `http://localhost:3000`; exact browser origin, no path |
| `COOKIE_SECURE` | `true` in Go; local Compose explicitly uses `false` |
| `APP_ENV` | Development exceptions require exactly `development` |
| `DEV_ENDPOINT_HOSTS` | Empty; comma-separated exact development hostnames |
| `LIMIT_PROJECTS` | 5 active projects per organization |
| `LIMIT_ENDPOINTS` | 10 endpoints across non-archived projects per organization |
| `LIMIT_TARGETS` | 10; configurable downwards within the current API schema |
| `LIMIT_MONTHLY_EVENTS` | 10,000 accepted events per organization/UTC month |
| `LIMIT_ORGANIZATION_JOBS` | 10,000 active jobs per organization |
| `LIMIT_QUEUE_JOBS` | 100,000 active jobs globally |
| `STORAGE_LIMIT_BYTES` | 10 GiB database-size admission budget |
| `STORAGE_RESERVE_BYTES` | 256 MiB reserved below that budget for ongoing work |
| `RATE_INGEST_PER_MINUTE` | 60 requests per project per fixed UTC minute |
| `RATE_MUTATIONS_PER_MINUTE` | 30 owner mutations per minute |
| `RATE_LOGIN_PER_15_MINUTES` | 10 per account and direct client IP |

The storage reserve is an application admission margin, not an OS free-space
measurement. Monitor the actual database volume and provision space outside this
budget. Configure all replicas consistently; these are operator policy values,
not paid plans. Worker limits are 10 concurrent requests/process and 2/endpoint/process.

## Publish from your application

```sh
curl -i http://localhost:8080/v1/projects/PROJECT_UUID/events \
  -H 'Authorization: Bearer YOUR_INGESTION_TOKEN' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-123-paid-v1' \
  --data-binary '{"type":"order.paid","endpoint_ids":["ENDPOINT_UUID"],"data":{"order_id":"123"}}'
```

Replace the placeholders. This example contains no actual credentials. A 202
means envelope, URL snapshots, jobs, dedupe receipt and quota have committed
**together**. Retry uncertain publication with the same key and exact body bytes.
Changed whitespace counts as different bytes. Identical repeats consume no new
quota. A purged payload returns 410 during the dedupe horizon.

Delivery is at-least-once with six total attempts, a 24-hour generation deadline,
no ordering guarantee and no redirects. A 2xx is transport acceptance, not proof
of the receiver's business transaction. Receivers must verify signatures and
transactionally deduplicate by the authenticated body event ID. Producers need an
outbox if publication must be atomic with their own business transaction.

See [receiver contract](docs/RECEIVERS.md), [API contract](docs/api/openapi.yaml),
[code reading guide](docs/READING_GUIDE.md), [architecture decisions](docs/adr/016-implementation.md)
and [operations](docs/runbooks/operations.md).

## Verify changes

From `apps/api`:

```sh
gofmt -w .
go vet ./...
go test -race ./...
go build ./cmd/...
```

For PostgreSQL integration tests, point `TEST_DATABASE_URL` at a **disposable**
PostgreSQL 17 database, then run:

```sh
go test -race -p 1 -count=1 ./...
```

Integration tests create fixture organizations and retained evidence. They skip
explicitly when the DSN is absent. Use `-p 1` to avoid different test packages'
worker fixtures consuming one another's jobs in a shared disposable database.
Native PostgreSQL is required to validate multi-session locking semantics.

From `apps/web`:

```sh
npm ci
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:browser
```

Unit/DOM tests and browser UI fixtures mock API responses. Go integration tests
exercise the actual API/store/delivery path. CI additionally runs native PostgreSQL,
browser tests, container builds, Compose health checks and backup/restore smoke tests.
Those configured checks are not evidence that a remote CI run has passed.

## Stop and recover

`docker compose down` preserves the database volume. `docker compose down -v`
**deletes the local database**; use it only for a disposable installation.

A stopped worker leaves accepted events in PostgreSQL. Expired leases become
`unknown` attempt outcomes; bounded retry or visible terminal failure follows.
SIGTERM stops claims, drains for up to 15 seconds and then cancels sends.
See the operations runbook for cleanup, counter reconciliation and backup restore.

Billing, public registration, teams, Redis, message brokers, Kubernetes and cloud
commercial features are intentionally outside this PRD's self-hosted MVP.
