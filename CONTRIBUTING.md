# Contributing to Hookgarde

Read `docs/prd.md`, `docs/READING_GUIDE.md` and `docs/adr/016-implementation.md` before
changing delivery semantics. Keep changes focused and explain their effect on the
request/transaction/worker flow. Follow the README checks and include failure tests
for changes to security, persistence, idempotency and retry behavior.

Never commit credentials, event bodies from real users or full receiver responses.
Do not silently change signature bytes, retry budgets, acceptance guarantees,
replay URL snapshots or tenant boundaries. Update the contract and ADR first.

## License

Unless explicitly stated otherwise, contributions intentionally submitted for
inclusion in Hookgarde are offered under the Apache License 2.0, as described in
section 5 of [LICENSE](LICENSE). Preserve applicable copyright and third-party
license notices. See [NOTICE](NOTICE) for the project copyright notice.
