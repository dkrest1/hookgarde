# Contributing to Hookgarde

Read `docs/prd.md`, `docs/READING_GUIDE.md` and `docs/adr/016-implementation.md` before
changing delivery semantics. Keep changes focused and explain their effect on the
request/transaction/worker flow. Follow the README checks and include failure tests
for changes to security, persistence, idempotency and retry behavior.

Never commit credentials, event bodies from real users or full receiver responses.
Do not silently change signature bytes, retry budgets, acceptance guarantees,
replay URL snapshots or tenant boundaries. Update the contract and ADR first.

The owner has not chosen a license. Contact the repository owner before proposing
external redistribution; this document does not grant a license.
