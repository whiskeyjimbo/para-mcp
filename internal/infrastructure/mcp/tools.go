package mcp

import (
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
)

func toolNoteGet() mcplib.Tool {
	return mcplib.NewTool("note_get",
		mcplib.WithDescription("Read a note by scope and path."),
		mcplib.WithString("scope", mcplib.Required(), mcplib.Description("Vault scope, e.g. personal")),
		mcplib.WithString("path", mcplib.Required(), mcplib.Description("Vault-relative path, e.g. projects/foo.md")),
	)
}

func toolNoteCreate() mcplib.Tool {
	return mcplib.NewTool("note_create",
		mcplib.WithDescription("Create a new note. Mints a stable NoteID automatically."),
		mcplib.WithString("path", mcplib.Required(), mcplib.Description("Vault-relative path, e.g. projects/foo.md")),
		mcplib.WithString("title", mcplib.Description("Note title")),
		mcplib.WithString("body", mcplib.Description("Markdown body")),
		mcplib.WithString("status", mcplib.Description("Note status, e.g. active")),
		mcplib.WithString("area", mcplib.Description("PARA area this note belongs to")),
		mcplib.WithString("project", mcplib.Description("PARA project this note belongs to")),
		mcplib.WithArray("tags", mcplib.WithStringItems(), mcplib.Description("Tags to apply")),
	)
}

func toolNoteUpdateBody() mcplib.Tool {
	return mcplib.NewTool("note_update_body",
		mcplib.WithDescription("Replace a note's body. Requires current ETag to prevent lost updates."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithString("body", mcplib.Required()),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read; omit to force-overwrite")),
	)
}

func toolNotePatchFrontMatter() mcplib.Tool {
	return mcplib.NewTool("note_patch_frontmatter",
		mcplib.WithDescription("Merge fields into a note's frontmatter. Only listed keys are changed."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithObject("fields", mcplib.Required(), mcplib.Description("Key-value pairs to merge, e.g. {\"status\":\"done\"}")),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read")),
	)
}

func toolNoteMove() mcplib.Tool {
	return mcplib.NewTool("note_move",
		mcplib.WithDescription("Move/rename a note to a new vault-relative path."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithString("new_path", mcplib.Required()),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read")),
	)
}

func toolNoteArchive() mcplib.Tool {
	return mcplib.NewTool("note_archive",
		mcplib.WithDescription("Move a note to archives/."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read")),
	)
}

func toolNoteDelete() mcplib.Tool {
	return mcplib.NewTool("note_delete",
		mcplib.WithDescription("Delete a note. soft=true moves to .trash; soft=false permanently removes."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithBoolean("soft", mcplib.Description("Soft-delete to .trash (default true)")),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read; omit to force-delete")),
	)
}

func toolNoteReplace() mcplib.Tool {
	return mcplib.NewTool("note_replace",
		mcplib.WithDescription("Atomically replace a note's body and patch its frontmatter in a single write. The note must exist. NoteID and CreatedAt are preserved."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithString("body", mcplib.Required(), mcplib.Description("New markdown body")),
		mcplib.WithObject("fields", mcplib.Required(), mcplib.Description("Frontmatter fields to patch, e.g. {\"title\":\"New Title\"}")),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read; omit for unconditional overwrite")),
	)
}

func toolNotePromote() mcplib.Tool {
	return mcplib.NewTool("note_promote",
		mcplib.WithDescription("Copy a note to another scope, minting a fresh NoteID at the destination. Optionally archive or delete the source."),
		mcplib.WithString("scope", mcplib.Required(), mcplib.Description("Source vault scope")),
		mcplib.WithString("path", mcplib.Required(), mcplib.Description("Vault-relative path of the source note")),
		mcplib.WithString("to_scope", mcplib.Required(), mcplib.Description("Destination vault scope")),
		mcplib.WithString("if_match", mcplib.Description("ETag of the source note; omit to skip precondition check")),
		mcplib.WithBoolean("keep_source", mcplib.Description("Keep the source note after promotion (default false = archive source)")),
		mcplib.WithString("on_conflict", mcplib.Enum("error", "overwrite"), mcplib.Description("What to do if the destination path already exists (default: error)")),
		mcplib.WithString("idempotency_key", mcplib.Description("Idempotency key for safe retry")),
	)
}

func toolNotesList() mcplib.Tool {
	return mcplib.NewTool("notes_list",
		mcplib.WithDescription("List notes with optional filtering, sorting, and pagination."),
		mcplib.WithString("status", mcplib.Description("Filter by status")),
		mcplib.WithString("area", mcplib.Description("Filter by area")),
		mcplib.WithString("project", mcplib.Description("Filter by project")),
		mcplib.WithArray("tags", mcplib.WithStringItems(), mcplib.Description("All-of tag filter")),
		mcplib.WithArray("categories", mcplib.WithStringItems(), mcplib.Description("Limit to PARA categories")),
		mcplib.WithString("sort",
			mcplib.Enum(string(domain.SortByUpdated), string(domain.SortByCreated), string(domain.SortByTitle)),
			mcplib.Description("Sort field"),
		),
		mcplib.WithBoolean("desc", mcplib.Description("Sort descending")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (1-100, default 20)")),
		mcplib.WithNumber("offset", mcplib.Description("Pagination offset (max 500; prefer cursor)")),
		mcplib.WithString("cursor", mcplib.Description("Opaque pagination cursor from a previous notes_list response")),
	)
}

func toolNotesSearch() mcplib.Tool {
	return mcplib.NewTool("notes_search",
		mcplib.WithDescription("Search notes. Mode selects retrieval strategy: lexical (BM25), semantic (vector), or hybrid (RRF fusion). Defaults to hybrid when semantic infra is available, else lexical."),
		mcplib.WithString("text", mcplib.Required(), mcplib.Description("Search query")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 10)")),
		mcplib.WithString("mode",
			mcplib.Enum(string(domain.SearchModeLexical), string(domain.SearchModeSemantic), string(domain.SearchModeHybrid)),
			mcplib.Description("Retrieval mode: lexical|semantic|hybrid"),
		),
	)
}

func toolWaitForIndex() mcplib.Tool {
	return mcplib.NewTool("wait_for_index",
		mcplib.WithDescription("Block until the given note reaches a terminal IndexState (indexed|failed|skipped) or the timeout elapses."),
		mcplib.WithString("scope", mcplib.Required(), mcplib.Description("Scope of the note")),
		mcplib.WithString("path", mcplib.Required(), mcplib.Description("Path of the note")),
		mcplib.WithNumber("index_timeout_ms", mcplib.Description("Max wait in ms (default 3000, max 10000)")),
	)
}

func toolNotesHybridSearch() mcplib.Tool {
	return mcplib.NewTool("notes_hybrid_search",
		mcplib.WithDescription("Hybrid BM25 + vector search fused via Reciprocal Rank Fusion. Falls back to lexical-only when semantic infra is unavailable."),
		mcplib.WithString("query", mcplib.Required(), mcplib.Description("Search query")),
		mcplib.WithArray("scopes", mcplib.WithStringItems(), mcplib.Description("Restrict to these scope IDs")),
		mcplib.WithArray("categories", mcplib.WithStringItems(), mcplib.Description("Limit to PARA categories")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 10)")),
	)
}

func toolNotesSemanticSearch() mcplib.Tool {
	return mcplib.NewTool("notes_semantic_search",
		mcplib.WithDescription("Vector similarity search over note bodies. Returns notes whose meaning matches the query, even without keyword overlap."),
		mcplib.WithString("query", mcplib.Required(), mcplib.Description("Natural-language query")),
		mcplib.WithArray("scopes", mcplib.WithStringItems(), mcplib.Description("Restrict to these scope IDs")),
		mcplib.WithArray("categories", mcplib.WithStringItems(), mcplib.Description("Limit to PARA categories")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 10)")),
		mcplib.WithNumber("threshold", mcplib.Description("Cosine-similarity floor in [0,1]; 0 disables")),
		mcplib.WithString("body",
			mcplib.Enum(string(domain.BodyNever), string(domain.BodyOnDemand)),
			mcplib.Description("Body policy: never (default) or on_demand (top-3 with bodies when no threshold)"),
		),
	)
}

func toolVaultStats() mcplib.Tool {
	return mcplib.NewTool("vault_stats",
		mcplib.WithDescription("Return aggregate note counts by PARA category."),
	)
}

func toolNotesBacklinks() mcplib.Tool {
	return mcplib.NewTool("notes_backlinks",
		mcplib.WithDescription("Return notes that contain a wikilink pointing at the given note."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithBoolean("include_assets", mcplib.Description("Include ![[...]] asset-embed references (default false)")),
	)
}

func toolNotesRelated() mcplib.Tool {
	return mcplib.NewTool("notes_related",
		mcplib.WithDescription("Return notes related by tag, area, and project overlap, scored by overlap count."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 10)")),
	)
}

func toolNotesStale() mcplib.Tool {
	return mcplib.NewTool("notes_stale",
		mcplib.WithDescription("Return notes not updated within the given number of days."),
		mcplib.WithNumber("days", mcplib.Required(), mcplib.Description("Return notes not updated in this many days")),
		mcplib.WithString("status", mcplib.Description("Filter by status")),
		mcplib.WithArray("categories", mcplib.WithStringItems(), mcplib.Description("Limit to PARA categories")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 20)")),
	)
}

func toolVaultHealth() mcplib.Tool {
	return mcplib.NewTool("vault_health",
		mcplib.WithDescription("Return vault diagnostic info: case collisions, unrecognized files, sync conflicts, watcher status."),
	)
}

func toolVaultRescan() mcplib.Tool {
	return mcplib.NewTool("vault_rescan",
		mcplib.WithDescription("Trigger an immediate vault rescan. Mints IDs for any newly discovered notes."),
	)
}

func toolNotesCreateBatch() mcplib.Tool {
	return mcplib.NewTool("notes_create_batch",
		mcplib.WithDescription("Create multiple notes. Each note is independent: one failure does not prevent others from being created."),
		mcplib.WithArray("notes", mcplib.Required(), mcplib.Description("array of objects"), mcplib.Description(`Array of note objects. Each must have "path"; optional: "title", "body", "status", "area", "project", "tags"`)),
	)
}

func toolVaultListScopes() mcplib.Tool {
	return mcplib.NewTool("vault_list_scopes",
		mcplib.WithDescription("List all vault scopes visible to this server, with their capabilities."),
	)
}

func toolNotesUpdateBatch() mcplib.Tool {
	return mcplib.NewTool("notes_update_batch",
		mcplib.WithDescription("Update bodies for multiple notes. Each note is independent: one failure does not affect siblings."),
		mcplib.WithArray("notes", mcplib.Required(), mcplib.Description("array of objects"), mcplib.Description(`Array of objects with "scope", "path", "body"; optional "if_match"`)),
	)
}

func toolNotesPatchFrontMatterBatch() mcplib.Tool {
	return mcplib.NewTool("notes_patch_frontmatter_batch",
		mcplib.WithDescription("Patch frontmatter for multiple notes. Each note is independent: one failure does not affect siblings."),
		mcplib.WithArray("notes", mcplib.Required(), mcplib.Description("array of objects"), mcplib.Description(`Array of objects with "scope", "path", "fields"; optional "if_match"`)),
	)
}

func toolAuditSearch() mcplib.Tool {
	return mcplib.NewTool("audit_search",
		mcplib.WithDescription("Search the immutable audit log. Admin only. Requires expose_admin_tools=true and admin role on the queried scope."),
		mcplib.WithString("scope", mcplib.Required(), mcplib.Description("Scope to query audit rows for. Caller must hold admin role on this scope.")),
		mcplib.WithString("actor", mcplib.Description("Filter by actor identity (exact match)")),
		mcplib.WithString("action", mcplib.Description("Filter by action name (exact match)")),
		mcplib.WithString("outcome", mcplib.Description("Filter by outcome: ok or error")),
		mcplib.WithString("since", mcplib.Description("RFC3339 lower bound (inclusive)")),
		mcplib.WithString("until", mcplib.Description("RFC3339 upper bound (inclusive)")),
		mcplib.WithNumber("limit", mcplib.Description("Max rows to return (default 50)")),
		mcplib.WithNumber("offset", mcplib.Description("Pagination offset")),
	)
}
