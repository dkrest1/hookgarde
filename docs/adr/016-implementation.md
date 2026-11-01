# ADR 016: self-hosted alpha implementation choices

Status: implemented baseline for owner review. Product scope: PRD 2.0. The owner
explicitly changed the learning workflow to complete assistant implementation
followed by reading, testing and modification. Billing and Kubernetes stay deferred.

## Shared queue and transactions

PostgreSQL owns event acceptance, quota, queue slots and attempt evidence. There
is no broker or second internal outbox. The producer's business transaction is
outside Hookgarde's durability guarantee and may require its own outbox.

All queue mutations acquire shared capacity first, then organization, project,
endpoint and job policy locks. A shared capacity lock serializes state-changing
queue transactions, including claims/completions/recovery. It simplifies the first
correct implementation and avoids the inconsistent lock-order wording in the
supporting architecture/database documents. No database transaction spans network I/O.
Claim selection uses organization last-claim time, due time and bounded local
concurrency; selected jobs use SKIP LOCKED and eligibility is rechecked.

Consequence: shared lock contention bounds throughput. Do not claim this design
scales indefinitely. Benchmark contention before replacing counters with a more
complex reservation protocol. Introduce no broker solely to hide this limitation.

## Delivery and response evidence

The v2 exponential policy is adopted: six attempts, factor four from ten seconds,
0-20% positive jitter, a 24-hour generation deadline, and bounded Retry-After.
A reserved claim consumes an attempt even if a crash prevents a confirmed request.
Lease recovery records unknown; tokens fence DB completion but cannot stop an
already sent external duplicate. Receiver 2xx remains success despite a body-read
failure. Redirects and transport-level POST replay are disabled.

Receiver bodies can contain echoed credentials and event data. This baseline
stores an allowlisted byte-count summary instead of arbitrary response text.
It samples at most 16 KiB; the stored preview stays below 4 KiB. This is a
conservative sanitization choice within the PRD's sanitized-preview requirement.
It sacrifices response-body troubleshooting detail for predictable redaction.

## Credential encoding and operational access

Signing secrets encode 32 random bytes as unpadded base64url. HMAC uses the decoded
bytes. AES-GCM binds ciphertext to organization, project, endpoint and key identity.
The external encryption master key is required at startup and restore. Rotating
that master key requires a reviewed re-encryption migration; changing the env var
alone makes old signing secrets unreadable.

Browser sessions use opaque HttpOnly/SameSite cookies, hashed tokens and 24-hour
absolute expiry. Secure cookies are the default; the localhost demo explicitly
opts out. Mutations require exact Origin and CSRF. Direct client IP is used for
login throttling; forwarded IP headers are not trusted. The Next proxy therefore
shares an IP login bucket, a conservative self-hosted limit to revisit for managed
hosting with a trusted proxy configuration.

Health readiness and metrics are private operational interfaces. Compose exposes
the API only on host loopback, and the browser proxy exposes only `/v1`. A reverse
proxy must preserve that operational boundary. Public registration remains absent.

## Storage and observability

Admission uses PostgreSQL database size versus an operator budget minus a reserve.
This measures database allocation, not filesystem free space; operators monitor
volume free space independently. The reserve leaves room for attempts and cleanup.
All replicas must share policy settings. API/worker log structured identifiers and
allowlisted error classes, never request bodies, tokens or raw receiver errors.
Basic Prometheus metrics are private. Full distributed tracing and deployment SLAs
require a later operational validation milestone; no such evidence is claimed here.

## Release gate

The owner selected Apache-2.0 for the current core; see [ADR 017](017-apache-2-license.md).
The future proprietary feature boundary remains undecided. Native PostgreSQL concurrency tests,
real browser checks, Compose cold start, restore drill, security review and
recorded load measurement must pass before calling this release production-ready.
See `docs/VERIFICATION.md`; configured CI is not a completed remote run.
