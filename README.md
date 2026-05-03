# paras

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.26%2B-blue)](go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/whiskeyjimbo/paras)](https://goreportcard.com/report/github.com/whiskeyjimbo/paras)

A federation-aware [MCP](https://modelcontextprotocol.io/) server for [PARA](https://fortelabs.com/blog/para/)-structured markdown vaults.

Paras gives Claude Desktop — and any MCP client — a structured, token-efficient interface to a local vault of markdown notes. Notes are plain `.md` files with YAML frontmatter. Paras indexes them, watches for changes, and exposes 20+ MCP tools covering the full lifecycle: single-note CRUD, batch operations, full-text search, backlink graphs, and cross-vault federation.

```
vault/
  projects/    active projects with a defined outcome
  areas/       ongoing responsibilities without an end date
  resources/   reference material and notes
  archives/    completed or inactive items
```

---

## Contents

- [Why Paras](#why-paras)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [MCP Integration](#mcp-integration)
- [Vault Structure](#vault-structure)
- [MCP Tools](#mcp-tools)
- [Design Principles](#design-principles)
- [Concurrency Model](#concurrency-model)
- [Federation Mode](#federation-mode)
- [Authentication (Server Mode)](#authentication-server-mode)
- [Architecture](#architecture)
- [Development](#development)
- [Contributing](#contributing)
- [Roadmap](#roadmap)
- [License](#license)

---

## Why Paras

Most "Claude + notes" integrations dump raw file lists into the context window and let the model filter. That burns tokens, slows responses, and breaks when vaults grow large.

Paras pushes work to the server:

- **Structured filters on every query** — category, status, tags, area, project, date ranges, full-text BM25 search. Claude asks for what it needs; paras returns exactly that.
- **Summaries by default, bodies on demand** — list and search tools return lightweight metadata. Full markdown only comes back when you call `note_get`.
- **Aggregations as first-class tools** — vault stats, backlinks, staleness, and related-note scoring are dedicated tool calls, not assembled from raw lists.
- **Stable NoteIDs** — a ULID is minted into frontmatter on create and survives renames, moves, and re-indexes. Claude can reference a note by ID across sessions.
- **Multi-vault federation** — personal, team, and org vaults federate into a single MCP server. Reads fan out; writes route to the owning vault. Remote vaults push invalidations via SSE so caches stay warm without polling.

---

## Installation

Requires Go 1.26+.

```bash
git clone https://github.com/whiskeyjimbo/paras
cd paras
go build -o paras ./cmd/paras
```

Or install directly:

```bash
go install github.com/whiskeyjimbo/paras/cmd/paras@latest
```

---

## Quick Start

Point paras at your vault directory and add it to your MCP config:

```bash
# Test that it works
./paras --vault ~/notes --scope personal
```

```json
{
  "mcpServers": {
    "paras": {
      "command": "/path/to/paras",
      "args": ["--vault", "/path/to/your/vault", "--scope", "personal"]
    }
  }
}
```

Paras will create an index on first run and keep it in sync via fsnotify. A `vault_rescan` tool call forces a full re-index and mints stable IDs for any notes that don't have one yet.

---

## MCP Integration

### Single-vault mode

Run paras in stdio mode (default). The binary speaks the MCP protocol over stdin/stdout.

```json
{
  "mcpServers": {
    "paras": {
      "command": "/path/to/paras",
      "args": ["--vault", "/path/to/your/vault", "--scope", "personal"]
    }
  }
}
```

### Federation mode (multi-vault)

Run one paras process per vault in HTTP server mode:

```bash
paras --vault /path/to/team-vault --scope team --addr :8080
```

Then run a federation gateway in stdio mode with a config file:

```yaml
# federation.yaml
local:
  vault: /path/to/your-vault
  scope: personal
  tombstone_store: /path/to/tombstones.json  # persists soft-deletes across restarts
remotes:
  - scope: team
    canonical_remote: team    # scope name on the remote server (defaults to scope)
    url: http://team-host:8080/mcp
```

```json
{
  "mcpServers": {
    "paras": {
      "command": "/path/to/paras",
      "args": ["--config", "/path/to/federation.yaml"]
    }
  }
}
```

The gateway fans out reads across all registered vaults and routes writes to the vault that owns the target scope. Remote vaults advertising the `Watch` capability are subscribed via SSE (`/events`) so their summary cache invalidates on push rather than waiting for a TTL expiry.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--vault` | *(required if no --config)* | Path to the vault root directory |
| `--scope` | `personal` | Scope identifier for this vault instance |
| `--config` | *(required if no --vault)* | Path to federation config YAML |
| `--addr` | *(empty)* | HTTP listen address (e.g. `:8080`); enables server mode |
| `--auth-mode` | *(empty)* | `bearer` or `oidc` (empty = no auth; dev/stdio only) |
| `--jwks-endpoint` | *(empty)* | OIDC provider JWKS URL |
| `--bearer-tokens-file` | *(empty)* | JSON file mapping bearer tokens to caller identities |
| `--require-promotion-approval` | `false` | Gate `note_promote` on explicit approval before executing |

---

## Vault Structure

A PARA vault is a directory with four top-level folders:

```
vault/
  projects/    active projects with a defined outcome
  areas/       ongoing responsibilities without an end date
  resources/   reference material and notes
  archives/    completed or inactive items
```

Notes are standard Markdown files. Paras reads and writes YAML frontmatter:

```markdown
---
title: Migrate auth service to Temporal
status: active
area: engineering
project: auth-migration
tags: [temporal, golang, auth]
id: 01JT9XKPQ3...       # stable ULID minted on create; survives renames
---

Body goes here. [[wikilinks]] to other notes are indexed automatically.
```

Paras ignores sync-conflict files (Dropbox, Google Drive, OneDrive patterns) and detects case collisions on case-sensitive filesystems via `vault_health`.

---

## MCP Tools

### Single-Note Operations

| Tool | Description |
|------|-------------|
| `note_get` | Read a note by scope and path |
| `note_create` | Create a note; mints a stable NoteID automatically |
| `note_update_body` | Replace a note's body (ETag-gated) |
| `note_patch_frontmatter` | Merge fields into frontmatter; only named keys change |
| `note_move` | Move/rename a note to a new path |
| `note_archive` | Move a note into `archives/` |
| `note_delete` | Delete; `soft=true` moves to `.trash` (default), `soft=false` removes permanently |
| `note_promote` | Copy a note across scopes with a fresh NoteID; `on_conflict: error\|overwrite`; `idempotency_key` for safe retries *(federation mode only)* |

### Query & Discovery

| Tool | Description |
|------|-------------|
| `notes_list` | Filter, sort, and paginate note summaries |
| `notes_search` | BM25 full-text search over titles and bodies (Porter stemming) |
| `notes_backlinks` | Notes that contain a `[[wikilink]]` to the given note |
| `notes_related` | Notes scored by tag/area/project overlap |
| `notes_stale` | Notes not updated within N days |

### Vault Management

| Tool | Description |
|------|-------------|
| `vault_stats` | Note counts by PARA category |
| `vault_health` | Diagnostics: case collisions, unrecognized files, watcher status |
| `vault_rescan` | Force a vault re-index; mints IDs for newly discovered notes |
| `vault_list_scopes` | List all registered scopes and their capabilities |

### Batch Operations

Each batch tool processes items independently — one failure does not affect siblings.

| Tool | Description |
|------|-------------|
| `notes_create_batch` | Create multiple notes in one call |
| `notes_update_batch` | Update bodies for multiple notes |
| `notes_patch_frontmatter_batch` | Patch frontmatter for multiple notes |

---

## Design Principles

**Summarize by default, hydrate on demand.** List and query tools return lightweight summaries (path, title, tags, status, dates). Full body is only returned by `note_get`.

**Push filtering to the server.** Every query tool accepts structured filters. Notes never land in Claude's context window to be filtered there.

**Aggregations are first-class.** Stats, health, staleness, related-note scoring, and backlinks are dedicated tool calls — not assembled from raw lists.

**Single-call mutations.** Creating or updating a note is one call. No read-modify-write cycles.

**Stable identity through changes.** NoteIDs (ULIDs) are stored in frontmatter and survive renames, moves, and re-indexes. Paths are addresses; IDs are identities.

---

## Concurrency Model

All writes to a given note are serialized through a per-path actor pool. This prevents races between the filesystem watcher (which maintains the search index) and concurrent mutations.

Mutation responses include an **ETag** — a Blake3 hash over the note's canonical frontmatter and body. Pass it back as `if_match` on the next write to get optimistic concurrency: the server rejects stale writes with a `conflict` error instead of silently overwriting.

```
// create
{ "etag": "01JT9XKP...", "path": "projects/foo.md", "title": "Foo", ... }

// update with ETag -- succeeds only if nothing changed since the read
note_update_body(scope, path, body, if_match="01JT9XKP...")

// omit if_match to force-overwrite
note_update_body(scope, path, body)
```

---

## Federation Mode

Federation lets you stack personal, team, and org vaults behind a single MCP endpoint.

- **Reads fan out** — queries run across all registered vaults and results are merged. If one remote fails, the response still succeeds with the remaining vaults plus a `PartialFailure` field.
- **Writes route by scope** — mutations go to the vault that owns the target scope.
- **Pagination is cursor-based** — cursors are HMAC-signed and opaque; they carry scope state across config reloads.
- **Cache invalidation via SSE** — remote vaults with a `/events` endpoint push change events. Paras subscribes and invalidates its summary cache immediately rather than waiting for the TTL (30s summaries, 5min bodies).
- **`note_promote` across scopes** — promotes a note from one vault to another with a fresh NoteID, idempotency key support, and configurable conflict resolution.

---

## Authentication (Server Mode)

Authentication applies only in HTTP server mode (`--addr`). Stdio mode has no network surface.

### Bearer tokens

Map raw tokens to named caller identities:

```bash
paras --vault /path/to/vault --addr :8080 \
  --auth-mode bearer \
  --bearer-tokens-file tokens.json
```

```json
{
  "tokens": {
    "secret-token-abc": "alice",
    "secret-token-xyz": "bob"
  }
}
```

### OIDC

Validate JWTs against a provider's JWKS endpoint:

```bash
paras --vault /path/to/vault --addr :8080 \
  --auth-mode oidc \
  --jwks-endpoint https://provider.example.com/.well-known/jwks.json
```

### RBAC

Callers are assigned per-scope roles: `Owner`, `Maintainer`, `Contributor`, `Viewer`. Roles gate destructive operations (`note_delete`, `note_promote`) and batch writes. The `AllowedScopes` pre-filter is always resolved server-side — callers cannot self-elevate.

---

## Architecture

### Request Flow

```mermaid
flowchart TD
    A([Claude Desktop]) -->|stdio MCP| B

    subgraph infra ["infrastructure/mcp"]
        B[Tool Handlers]
    end

    B --> C

    subgraph app ["application"]
        C[NoteService\nvalidate paths · enforce PARA root\napply templates · mint IDs · check scope]
    end

    C -->|ports.Vault| D

    subgraph storage ["infrastructure/storage/localvault"]
        D[LocalVault]
        D --- E[BM25 FTS Index\nkljensen/snowball]
        D --- F[Backlink Graph\nwikilink index]
        D --- G[Actor Pool\nper-path mutation serialization]
        D --- H[fsnotify Watcher]
    end

    subgraph domain ["core/domain"]
        I[Note · Filter · MutationResult\nNoteRef · ETag · PARA categories]
    end

    C -.->|depends on| I
    D -.->|depends on| I
```

### Code Layout

```
paras/
  cmd/paras/              # binary entrypoint, flag parsing, auth wiring
  cmd/wirecheck/          # linter: AllowedScopes must never be wire-sourced
  internal/
    core/
      domain/             # Note, Filter, NoteRef, ETag, scoring, path normalization
      ports/              # Vault, Index, Embedder, VectorStore interfaces
    application/          # NoteService, VaultRegistry, FederationService
    infrastructure/
      mcp/                # tool handlers, auth middleware, SSE endpoint
      storage/
        localvault/       # filesystem adapter, BM25 index, fsnotify watcher
        tombstone/        # soft-delete persistence across restarts
      actor/              # per-path goroutine pool for serialized writes
      index/              # BM25 inverted index, title-field boost
    server/
      auth/               # bearer/OIDC middleware
      rbac/               # role-based access control
    ctxutil/              # request ID correlation, context helpers
  docs/
    DESIGN.md             # system architecture and principles
    design/               # subsystem design docs (federation, auth, local vault)
    decisions/            # ADRs
    features/             # feature specs and acceptance tests
```

### Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/mark3labs/mcp-go` | MCP protocol |
| `github.com/kljensen/snowball` | Porter stemming for BM25 search |
| `github.com/fsnotify/fsnotify` | Filesystem watcher |
| `github.com/oklog/ulid/v2` | NoteID generation |
| `github.com/zeebo/blake3` | ETag hashing |
| `github.com/lestrrat-go/jwx/v2` | JWT/OIDC validation |
| `github.com/jackc/pgx/v5` | PostgreSQL backend (server mode) |

---

## Development

```bash
# Build
go build ./cmd/paras

# Test (race detector on)
go test -race ./...

# Format
go fmt ./...

# Run the wirecheck linter
go vet -vettool=$(go build -o /tmp/wirecheck ./cmd/wirecheck && echo /tmp/wirecheck) ./...
```

Tests use [testcontainers](https://golang.testcontainers.org/) for integration tests that need a real Postgres instance. Docker or a compatible runtime must be available for those tests to run; unit tests pass without it.

---

## License

Apache 2.0. See [LICENSE](LICENSE).
