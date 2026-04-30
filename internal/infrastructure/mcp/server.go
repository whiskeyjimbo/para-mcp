// Package mcp wires NoteService to the MCP tool surface via stdio transport.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// NotePort is the application interface consumed by MCP tool handlers.
type NotePort interface {
	Get(ctx context.Context, ref domain.NoteRef) (domain.Note, error)
	Create(ctx context.Context, in domain.CreateInput) (domain.NoteSummary, error)
	UpdateBody(ctx context.Context, ref domain.NoteRef, body, ifMatch string) (domain.NoteSummary, error)
	PatchFrontMatter(ctx context.Context, ref domain.NoteRef, fields map[string]any, ifMatch string) (domain.NoteSummary, error)
	Move(ctx context.Context, ref domain.NoteRef, newPath string, ifMatch string) (domain.NoteSummary, error)
	Delete(ctx context.Context, ref domain.NoteRef, soft bool) error
	Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error)
	Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error)
	Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error)
	Related(ctx context.Context, ref domain.NoteRef, limit int, filter domain.Filter) ([]domain.RankedNote, error)
	Stats(ctx context.Context) (domain.VaultStats, error)
	Health(ctx context.Context) (domain.VaultHealth, error)
	Rescan(ctx context.Context) error
	CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error)
	UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error)
	PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error)
}

// ScopesFunc resolves the permitted scopes for a request.
type ScopesFunc func(ctx context.Context) []domain.ScopeID

// Option configures a Build call.
type Option func(*buildConfig)

type buildConfig struct {
	scopesFn      ScopesFunc
	serverName    string
	serverVersion string
	clock         func() time.Time
}

// WithScopesFunc sets the scope resolver (default: personal only).
func WithScopesFunc(fn ScopesFunc) Option {
	return func(c *buildConfig) { c.scopesFn = fn }
}

// WithServerName overrides the MCP server name (default: "paras").
func WithServerName(name string) Option {
	return func(c *buildConfig) { c.serverName = name }
}

// WithServerVersion overrides the MCP server version (default: "0.1.0").
func WithServerVersion(v string) Option {
	return func(c *buildConfig) { c.serverVersion = v }
}

// WithClock overrides the time source used by the stale notes handler (default: time.Now).
func WithClock(fn func() time.Time) Option {
	return func(c *buildConfig) { c.clock = fn }
}

func personalOnly(_ context.Context) []domain.ScopeID {
	return []domain.ScopeID{"personal"}
}

// Build constructs and returns an MCPServer wired to svc.
func Build(svc NotePort, opts ...Option) *mcpserver.MCPServer {
	cfg := buildConfig{
		scopesFn:      personalOnly,
		serverName:    "paras",
		serverVersion: "0.1.0",
		clock:         time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}

	s := mcpserver.NewMCPServer(
		cfg.serverName,
		cfg.serverVersion,
		mcpserver.WithToolCapabilities(true),
	)

	h := &handlers{svc: svc, scopes: cfg.scopesFn, clock: cfg.clock}

	s.AddTool(toolNoteGet(), h.noteGet)
	s.AddTool(toolNoteCreate(), h.noteCreate)
	s.AddTool(toolNoteUpdateBody(), h.noteUpdateBody)
	s.AddTool(toolNotePatchFrontMatter(), h.notePatchFrontMatter)
	s.AddTool(toolNoteMove(), h.noteMove)
	s.AddTool(toolNoteArchive(), h.noteArchive)
	s.AddTool(toolNoteDelete(), h.noteDelete)
	s.AddTool(toolNotesList(), h.notesList)
	s.AddTool(toolNotesSearch(), h.notesSearch)
	s.AddTool(toolVaultStats(), h.vaultStats)

	s.AddTool(toolNotesBacklinks(), h.notesBacklinks)
	s.AddTool(toolNotesRelated(), h.notesRelated)
	s.AddTool(toolNotesStale(), h.notesStale)
	s.AddTool(toolVaultHealth(), h.vaultHealth)
	s.AddTool(toolVaultRescan(), h.vaultRescan)
	s.AddTool(toolNotesCreateBatch(), h.notesCreateBatch)
	s.AddTool(toolNotesUpdateBatch(), h.notesUpdateBatch)
	s.AddTool(toolNotesPatchFrontMatterBatch(), h.notesPatchFrontMatterBatch)

	return s
}

type handlers struct {
	svc    NotePort
	scopes ScopesFunc
	clock  func() time.Time
}

func requireNoteRef(req mcplib.CallToolRequest) (domain.NoteRef, *mcplib.CallToolResult) {
	scope, err := req.RequireString("scope")
	if err != nil {
		return domain.NoteRef{}, mcplib.NewToolResultError(err.Error())
	}
	path, err := req.RequireString("path")
	if err != nil {
		return domain.NoteRef{}, mcplib.NewToolResultError(err.Error())
	}
	return domain.NoteRef{Scope: scope, Path: path}, nil
}

func toolNoteGet() mcplib.Tool {
	return mcplib.NewTool("note_get",
		mcplib.WithDescription("Read a note by scope and path."),
		mcplib.WithString("scope", mcplib.Required(), mcplib.Description("Vault scope, e.g. personal")),
		mcplib.WithString("path", mcplib.Required(), mcplib.Description("Vault-relative path, e.g. projects/foo.md")),
	)
}

func (h *handlers) noteGet(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	note, err := h.svc.Get(ctx, ref)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(note)
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

func (h *handlers) noteCreate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	in := domain.CreateInput{
		Path: path,
		FrontMatter: domain.FrontMatter{
			Title:   req.GetString("title", ""),
			Status:  req.GetString("status", ""),
			Area:    req.GetString("area", ""),
			Project: req.GetString("project", ""),
			Tags:    req.GetStringSlice("tags", nil),
		},
		Body: req.GetString("body", ""),
	}
	sum, err := h.svc.Create(ctx, in)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(sum)
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

func (h *handlers) noteUpdateBody(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	sum, err := h.svc.UpdateBody(ctx, ref, body, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(sum)
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

func (h *handlers) notePatchFrontMatter(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	raw, ok := req.GetArguments()["fields"]
	if !ok {
		return mcplib.NewToolResultError("fields required"), nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return mcplib.NewToolResultError("fields must be an object"), nil
	}
	sum, err := h.svc.PatchFrontMatter(ctx, ref, fields, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(sum)
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

func (h *handlers) noteMove(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	newPath, err := req.RequireString("new_path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	sum, err := h.svc.Move(ctx, ref, newPath, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(sum)
}

func toolNoteArchive() mcplib.Tool {
	return mcplib.NewTool("note_archive",
		mcplib.WithDescription("Move a note to archives/."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithString("if_match", mcplib.Description("ETag from last read")),
	)
}

func (h *handlers) noteArchive(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	newPath, err := domain.ArchivePath(ref.Path)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	sum, err := h.svc.Move(ctx, ref, newPath, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(sum)
}

func toolNoteDelete() mcplib.Tool {
	return mcplib.NewTool("note_delete",
		mcplib.WithDescription("Delete a note. soft=true moves to .trash; soft=false permanently removes."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithBoolean("soft", mcplib.Description("Soft-delete to .trash (default true)")),
	)
}

func (h *handlers) noteDelete(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	if err := h.svc.Delete(ctx, ref, req.GetBool("soft", true)); err != nil {
		return toolErr(err), nil
	}
	return mcplib.NewToolResultText("deleted"), nil
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
		mcplib.WithNumber("offset", mcplib.Description("Pagination offset")),
	)
}

func (h *handlers) notesList(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	f := domain.Filter{
		AllowedScopes: h.scopes(ctx),
		Status:        req.GetString("status", ""),
		Area:          req.GetString("area", ""),
		Project:       req.GetString("project", ""),
		Tags:          req.GetStringSlice("tags", nil),
	}
	for _, c := range req.GetStringSlice("categories", nil) {
		f.Categories = append(f.Categories, domain.Category(c))
	}
	result, err := h.svc.Query(ctx, domain.QueryRequest{
		Filter: f,
		Sort:   domain.SortField(req.GetString("sort", string(domain.SortByUpdated))),
		Desc:   req.GetBool("desc", false),
		Limit:  req.GetInt("limit", 20),
		Offset: req.GetInt("offset", 0),
	})
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func toolNotesSearch() mcplib.Tool {
	return mcplib.NewTool("notes_search",
		mcplib.WithDescription("Full-text BM25 search over note titles and bodies."),
		mcplib.WithString("text", mcplib.Required(), mcplib.Description("Search query")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 10)")),
	)
}

func (h *handlers) notesSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	results, err := h.svc.Search(ctx, text, domain.Filter{AllowedScopes: h.scopes(ctx)}, req.GetInt("limit", 10))
	if err != nil {
		return toolErr(err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
}

func toolVaultStats() mcplib.Tool {
	return mcplib.NewTool("vault_stats",
		mcplib.WithDescription("Return aggregate note counts by PARA category."),
	)
}

func (h *handlers) vaultStats(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	stats, err := h.svc.Stats(ctx)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(stats)
}

func toolNotesBacklinks() mcplib.Tool {
	return mcplib.NewTool("notes_backlinks",
		mcplib.WithDescription("Return notes that contain a wikilink pointing at the given note."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithBoolean("include_assets", mcplib.Description("Include ![[...]] asset-embed references (default false)")),
	)
}

func (h *handlers) notesBacklinks(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	entries, err := h.svc.Backlinks(ctx, ref, req.GetBool("include_assets", false),
		domain.Filter{AllowedScopes: h.scopes(ctx)})
	if err != nil {
		return toolErr(err), nil
	}
	if entries == nil {
		entries = []domain.BacklinkEntry{}
	}
	return jsonResult(entries)
}

func toolNotesRelated() mcplib.Tool {
	return mcplib.NewTool("notes_related",
		mcplib.WithDescription("Return notes related by tag, area, and project overlap, scored by overlap count."),
		mcplib.WithString("scope", mcplib.Required()),
		mcplib.WithString("path", mcplib.Required()),
		mcplib.WithNumber("limit", mcplib.Description("Max results (default 10)")),
	)
}

func (h *handlers) notesRelated(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	results, err := h.svc.Related(ctx, ref, req.GetInt("limit", 10), domain.Filter{AllowedScopes: h.scopes(ctx)})
	if err != nil {
		return toolErr(err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
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

func (h *handlers) notesStale(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	days := req.GetInt("days", 0)
	if days <= 0 {
		return mcplib.NewToolResultError("days must be > 0"), nil
	}
	cutoff := h.clock().AddDate(0, 0, -days)
	f := domain.Filter{
		AllowedScopes: h.scopes(ctx),
		Status:        req.GetString("status", ""),
		UpdatedBefore: &cutoff,
	}
	for _, c := range req.GetStringSlice("categories", nil) {
		f.Categories = append(f.Categories, domain.Category(c))
	}
	result, err := h.svc.Query(ctx, domain.QueryRequest{
		Filter: f,
		Sort:   domain.SortByUpdated,
		Desc:   false,
		Limit:  req.GetInt("limit", 20),
	})
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func toolVaultHealth() mcplib.Tool {
	return mcplib.NewTool("vault_health",
		mcplib.WithDescription("Return vault diagnostic info: case collisions, unrecognized files, sync conflicts, watcher status."),
	)
}

func (h *handlers) vaultHealth(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	health, err := h.svc.Health(ctx)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(health)
}

func toolVaultRescan() mcplib.Tool {
	return mcplib.NewTool("vault_rescan",
		mcplib.WithDescription("Trigger an immediate vault rescan. Mints IDs for any newly discovered notes."),
	)
}

func (h *handlers) vaultRescan(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if err := h.svc.Rescan(ctx); err != nil {
		return toolErr(err), nil
	}
	return mcplib.NewToolResultText("rescan complete"), nil
}

func toolNotesCreateBatch() mcplib.Tool {
	return mcplib.NewTool("notes_create_batch",
		mcplib.WithDescription("Create multiple notes. Each note is independent: one failure does not prevent others from being created."),
		mcplib.WithArray("notes", mcplib.Required(), mcplib.Description("array of objects"), mcplib.Description(`Array of note objects. Each must have "path"; optional: "title", "body", "status", "area", "project", "tags"`)),
	)
}

func (h *handlers) notesCreateBatch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	raw, ok := req.GetArguments()["notes"]
	if !ok {
		return mcplib.NewToolResultError("notes required"), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return mcplib.NewToolResultError("notes must be an array"), nil
	}
	inputs := make([]domain.CreateInput, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d] must be an object", i)), nil
		}
		path, _ := obj["path"].(string)
		if path == "" {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d].path required", i)), nil
		}
		in := domain.CreateInput{
			Path: path,
			Body: stringVal(obj, "body"),
			FrontMatter: domain.FrontMatter{
				Title:   stringVal(obj, "title"),
				Status:  stringVal(obj, "status"),
				Area:    stringVal(obj, "area"),
				Project: stringVal(obj, "project"),
				Tags:    stringSliceVal(obj, "tags"),
			},
		}
		inputs = append(inputs, in)
	}
	result, err := h.svc.CreateBatch(ctx, inputs)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func toolNotesUpdateBatch() mcplib.Tool {
	return mcplib.NewTool("notes_update_batch",
		mcplib.WithDescription("Update bodies for multiple notes. Each note is independent: one failure does not affect siblings."),
		mcplib.WithArray("notes", mcplib.Required(), mcplib.Description("array of objects"), mcplib.Description(`Array of objects with "scope", "path", "body"; optional "if_match"`)),
	)
}

func (h *handlers) notesUpdateBatch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	raw, ok := req.GetArguments()["notes"]
	if !ok {
		return mcplib.NewToolResultError("notes required"), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return mcplib.NewToolResultError("notes must be an array"), nil
	}
	inputs := make([]domain.BatchUpdateBodyInput, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d] must be an object", i)), nil
		}
		path, _ := obj["path"].(string)
		if path == "" {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d].path required", i)), nil
		}
		inputs = append(inputs, domain.BatchUpdateBodyInput{
			Path:    path,
			Body:    stringVal(obj, "body"),
			IfMatch: stringVal(obj, "if_match"),
		})
	}
	result, err := h.svc.UpdateBodyBatch(ctx, inputs)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func toolNotesPatchFrontMatterBatch() mcplib.Tool {
	return mcplib.NewTool("notes_patch_frontmatter_batch",
		mcplib.WithDescription("Patch frontmatter for multiple notes. Each note is independent: one failure does not affect siblings."),
		mcplib.WithArray("notes", mcplib.Required(), mcplib.Description("array of objects"), mcplib.Description(`Array of objects with "scope", "path", "fields"; optional "if_match"`)),
	)
}

func (h *handlers) notesPatchFrontMatterBatch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	raw, ok := req.GetArguments()["notes"]
	if !ok {
		return mcplib.NewToolResultError("notes required"), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return mcplib.NewToolResultError("notes must be an array"), nil
	}
	inputs := make([]domain.BatchPatchFrontMatterInput, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d] must be an object", i)), nil
		}
		path, _ := obj["path"].(string)
		if path == "" {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d].path required", i)), nil
		}
		fields, _ := obj["fields"].(map[string]any)
		if fields == nil {
			return mcplib.NewToolResultError(fmt.Sprintf("notes[%d].fields required", i)), nil
		}
		inputs = append(inputs, domain.BatchPatchFrontMatterInput{
			Path:    path,
			Fields:  fields,
			IfMatch: stringVal(obj, "if_match"),
		})
	}
	result, err := h.svc.PatchFrontMatterBatch(ctx, inputs)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func stringVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func stringSliceVal(m map[string]any, key string) []string {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func jsonResult(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(b)), nil
}

func toolErr(err error) *mcplib.CallToolResult {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return mcplib.NewToolResultError("not_found: " + err.Error())
	case errors.Is(err, domain.ErrConflict):
		return mcplib.NewToolResultError("conflict: " + err.Error())
	default:
		return mcplib.NewToolResultError(err.Error())
	}
}
