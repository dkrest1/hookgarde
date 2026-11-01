# Operating Hookgarde

## Startup and deployment

Back up the current database and encryption key. Build reviewed images, start DB,
run `docker compose run --rm migrate` explicitly, then start API/worker/web. Confirm
`/health/live` and `/health/ready` locally and perform a publish/inspect workflow.
A fresh installation must contain no preconfigured account or public signup.

Never disable PostgreSQL fsync or synchronous commit for production durability.
Use TLS for remote PostgreSQL, a private network, injected secrets and HTTPS at the
public edge. Restrict `/health/ready` and `/metrics` to operational access. Keep the
DB and API host bindings private; expose only the dashboard/proxied API via the
reviewed reverse proxy. Public endpoint registration requires abuse controls and
endpoint ownership verification before public onboarding.

Ingestion keys are independent of owner cookies. Do not expose ENCRYPTION_KEY or
DATABASE_URL to the browser. Do not put production event bodies in issue reports.
The default login IP bucket is shared behind the dashboard proxy; add a reviewed
trusted-proxy policy before a large multi-user hosted deployment.

## Failure diagnosis

- **503 on ingestion:** inspect DB availability, database-size budget, global/org
  queue slots and private metrics. Nothing is accepted without commit. Retry the
  same key and exact bytes after an uncertain result.
- **Oldest due age growing:** inspect worker health, paused endpoints, retries,
  connection pool, DB lock waits and outbound capacity. Accepted jobs remain durable.
- **Unknown attempt:** a lease expired before a confirmed persisted outcome. It may
  have reached the receiver. Do not change evidence to a fabricated failure/success.
- **No retry after failure:** inspect classification, reserved attempts and deadline.
  Six total attempts and 24 hours are finite; replay creates a separate generation.
- **Destination policy rejection:** check URL scheme/port and every DNS answer.
  Do not bypass TLS/SSRF checks to make a production destination work.
- **Signing key unavailable:** verify the master encryption key matches the DB
  backup. Never log or paste the key as part of diagnosis.

SIGTERM stops claims and drains sends up to 15 seconds before cancellation. The
Compose worker grace period is 25 seconds. A hard kill is recovered through leases.
A DB outage stops new claims; pending work remains in PostgreSQL.

## Cleanup and counters

Worker maintenance runs every second in bounded job batches. Seven days after all
generations finish, event envelopes and attempt previews are purged. Active/paused
nonterminal work retains bodies until terminal. A paused job still expires visibly
at its 24-hour deadline. Dedupe receipts last 90 days; purged payload retries/replays
return 410. Expired sessions/rate buckets and 90-day audit entries are cleaned up.

```sh
docker compose run --rm admin maintain
docker compose run --rm admin reconcile
```

Reconciliation recomputes queue counters while holding the shared capacity lock.
Run after investigating counter alerts or a restored database. It does not invent
missing jobs. Monitor queue-table bloat, autovacuum, database size and disk space.
Backups may retain data after live payload purge; publish a separate backup
retention/deletion policy before external launch.

## Backup and restore drill

Keep the encryption key in a separately secured backup. Without it the database
cannot decrypt signing keys. Use an encrypted backup store with a defined retention
policy. Example local commands, from the repository root:

```sh
mkdir -p backups
chmod 700 backups
docker compose exec -T db pg_dump -U hookgarde -d hookgarde -Fc > backups/hookgarde.dump
chmod 600 backups/hookgarde.dump
```

Test restoration into a **new disposable database**, never over a live one:

```sh
docker compose exec -T db createdb -U hookgarde restore_check
docker compose exec -T db pg_restore -U hookgarde -d restore_check --exit-on-error < backups/hookgarde.dump
docker compose exec -T db psql -U hookgarde -d restore_check -c 'SELECT max(version) FROM schema_migrations'
```

Then point isolated API/worker processes at `restore_check`, using the backed-up
encryption key and no unintended external sends. Verify counts, tenant access,
readiness and a controlled test delivery. Record elapsed restore time and oldest
backup age. The PRD's provisional 24-hour RPO / 4-hour RTO are targets, not proven
claims. A schema-only restore smoke in CI is not the full operational drill.

## Rollback and capacity

Stop new claims before a risky deployment. Keep compatible old binaries/images,
but never assume application rollback reverses destructive SQL. Migration 001 is
initial-only and checksum tracked. Later changes need expand/contract migrations,
backup and compatibility review. API/worker refuse unknown schema versions.

Record host CPU/RAM, connection pools, rate/quota overrides, endpoint mix and queue
size for load tests. Measure acceptance p95, time to first attempt, oldest due age,
unknowns, outcomes and DB lock waits. Validate the PRD load target separately; the
shared capacity lock is an intentional contention point. Per-process concurrency
caps do not provide a global outbound cap across worker replicas.
