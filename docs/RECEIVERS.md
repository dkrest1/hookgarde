# Receiver contract

Hookgarde sends the immutable JSON envelope `{id,type,project_id,occurred_at,
accepted_at,data}`. Retries and replay preserve the exact serialized bytes. Do not
parse and reserialize before checking the signature.

Headers:

- `Content-Type: application/json`
- `Webhook-Event-Id`: informational; authenticate the ID from the signed body.
- `Webhook-Delivery-Id`: identifies this generation.
- `Webhook-Attempt-Id`: identifies this reserved attempt.
- `Webhook-Signature: t=UNIX_SECONDS,kid=KEY_VERSION,v1=HEX_HMAC`

The disclosed secret is **base64url without padding, encoding 32 random bytes**.
Decode it before using it as the HMAC-SHA256 key. The message is decimal Unix
seconds, a literal dot, then the exact request body bytes. Digest encoding is
lowercase hexadecimal. Compare digests in constant time. Require the expected
scheme/version and a timestamp within five minutes, using an accurate system clock.
Reject malformed, duplicate or unexpected signature components.

Versioned keys are scoped to an endpoint. Normal rotation activates the new key
after ten minutes; retain the previous verification key for 24 hours after that
activation. Emergency rotation revokes old keys immediately. Save the replacement
secret before closing the one-time disclosure in the console.

After verification, transactionally deduplicate the **authenticated envelope ID**
with your business state change. Return a 2xx only after durable acceptance. If work
is asynchronous, persist your own queue before returning. Hookgarde records a 2xx
as transport acceptance, not completion of your business operation.

Transport failures, 408, 429 and 5xx may retry; other 4xx and all 3xx terminate.
There are six total attempts within 24 hours. Retry bases after failures 1-5 are
10, 40, 160, 640 and 2560 seconds, plus 0-20% positive jitter. On 429/503,
Retry-After is a minimum capped at six hours; the generation deadline still wins.

The receiver may see duplicates, including after successful acceptance and after
manual replay. There is no ordering guarantee. Keep dedupe state for your
business's required horizon; replay can happen later than normal retries.

Production destinations require HTTPS on port 443 and public IPs. Every attempt
revalidates DNS; mixed private/public answers are rejected. Redirects and
HTTP environment proxies are disabled. A private development receiver requires an
explicit exact-host allowlist and development mode.
