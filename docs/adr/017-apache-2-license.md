# ADR 017: Apache License 2.0 for the self-hosted core

Status: approved by the owner, 1 November 2026.

## Context

Hookgarde PRD 2.0 establishes an independently usable open-source self-hosted core
and a possible future managed cloud service. The license choice was explicitly
reserved for the owner, who has now approved Apache-2.0 and the copyright notice
"Copyright 2026 Oluwatosin Temitope Akande".

## Decision

License the current Hookgarde codebase under the unmodified Apache License 2.0.
Keep its full text in the root LICENSE file and the project copyright notice in
NOTICE. Identify Apache-2.0 in the web package metadata. Third-party dependencies
remain under their own licenses; this decision does not relicense their code.

Contributions intentionally submitted for inclusion follow section 5 of Apache-2.0
unless explicitly stated otherwise. No separate contributor agreement is introduced.

## Consequences

Recipients may use, modify, distribute and commercially host the licensed code,
subject to Apache-2.0 conditions. Contributors provide the license's express patent
grant. The license does not grant general permission to use project trademarks.
It does not require recipients to publish their modifications merely because they
host a service. Competing hosting is therefore permitted.

The managed cloud service may be sold. Any future proprietary code must have an
explicitly documented boundary and separate licensing; no such code or extra
restriction is designated by this decision. Existing Apache-licensed releases
retain their granted permissions.

This ADR records the owner's later license decision and supersedes earlier
"license pending" statements in the planning documents. Product and delivery
requirements are unchanged.

## Source

Canonical license: https://www.apache.org/licenses/LICENSE-2.0.txt
