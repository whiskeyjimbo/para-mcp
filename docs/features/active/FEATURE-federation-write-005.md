---
id: FEAT-005
title: "Federation write"
status: planned
subsystem: "federation"
surface: "api"
owner: ""
phase: "P4"
depends_on: ["FEAT-004"]
blocks: ["FEAT-006"]
files:
  - internal/application/federation.go
  - internal/infra/remotevault/vault.go
  - internal/infra/remotevault/cache.go
  - internal/mcp/handlers/notes.go
tests: []
related_adrs: ["ADR-0001", "ADR-0005"]
nfr_impact: ["reliability", "latency"]
last_updated: "2026-05-01"
---

# Federation write

> **Scope:** Owns capability definition, DoD, acceptance tests, and agent-facing implications for this feature. Not system architecture, scheduling, or decision rationale.

## Capability
Mutations against remote vaults with ETag-based optimistic concurrency. `note_promote` across scopes. SSE push for summary invalidation. Delete tombstones as lifecycle anchors for Phase 6a semantic pipeline.

## Definition Of Done (DoD)
- [x] ETag-based optimistic concurrency end-to-end on every mutating tool; `conflict` error returns `details.request_id`
- [x] `note_promote(ref, to_scope, if_match, keep_source, on_conflict, idempotency_key)` -- single call cross-scope copy; source ETag precondition; destination mints fresh NoteID; returns NoteSummary at new ref
- [x] Body cache with TTL; SSE push for summary invalidation when remote vault supports Watch capability
- [x] Delete = tombstone with `(NoteRef, NoteID)` keying; tombstone survives gateway restart; deleted notes do not reappear from stale summary cache
- [x] 3-vault round-trip test: personal -> team -> personal under concurrent edits; `conflict` surfaces cleanly; re-read+retry succeeds

## Acceptance Tests
- [x] Stale ETag on remote update returns `conflict` with `details.request_id`. Re-read returns current ETag. Retry with fresh ETag succeeds.
- [x] `note_promote` personal -> team: source archived (keep_source=false); destination has new NoteID independent of source.
- [x] `on_conflict: error` on promote when destination path already exists; `on_conflict: overwrite` replaces it.
- [x] Tombstones survive gateway restart: deleted note does not reappear in federated list after restart + summary cache refresh.
- [ ] SSE push from remote: summary cache invalidated within push latency; next list reflects update without waiting for 30s refresh. *(gap: paras-hsu)*

## Agent-Facing Implications
- Files likely touched: `internal/application/federation.go`, `internal/infra/remotevault/vault.go`, `internal/infra/remotevault/cache.go`
- Test files: `internal/application/federation_test.go`
- Migration or rollout notes: Requires both gateway and remote to support ETag on every mutating tool.

## Traceability
- Related subsystem design: [Federation](../../design/DESIGN-federation.md)
- Related ADRs: [ADR-0001 (ETag formula)](../../decisions/ADR-0001-etag-formula.md), [ADR-0005 (conflict resolution UX)](../../decisions/ADR-0005-conflict-resolution-ux.md)
- Related phase item: P4
