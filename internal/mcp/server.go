// Package mcp wires LocalVault to the MCP tool surface via stdio transport.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/domain"
	"github.com/whiskeyjimbo/paras/internal/vault"
)

// allowedScopes is the fixed scope list for a single personal vault.
var allowedScopes = []domain.ScopeID{"personal"}

// Build constructs and returns an MCPServer wired to svc.
func Build(svc *vault.NoteService, v domain.Vault) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer(
		"paras",
		"0.1.0",
		mcpserver.WithToolCapabilities(true),
	)

	h := &handlers{svc: svc, vault: v}

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

	return s
}

type handlers struct {
	svc   *vault.NoteService
	vault domain.Vault
}

func toolNoteGet() mcplib.Tool {
	return mcplib.NewTool("note_get",
		mcplib.WithDescription("Read a note by scope and path."),
		mcplib.WithString("scope", mcplib.Required(), mcplib.Description("Vault scope, e.g. personal")),
		mcplib.WithString("path", mcplib.Required(), mcplib.Description("Vault-relative path, e.g. projects/foo.md")),
	)
}

func (h *handlers) noteGet(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	scope, err := req.RequireString("scope")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	note, err := h.svc.Get(ctx, domain.NoteRef{Scope: scope, Path: path})
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
			Title:  req.GetString("title", ""),
			Status: req.GetString("status", ""),
			Tags:   req.GetStringSlice("tags", nil),
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
	scope, _ := req.RequireString("scope")
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	sum, err := h.svc.UpdateBody(ctx, domain.NoteRef{Scope: scope, Path: path}, body, req.GetString("if_match", ""))
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
	scope, _ := req.RequireString("scope")
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	raw, ok := req.GetArguments()["fields"]
	if !ok {
		return mcplib.NewToolResultError("fields required"), nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return mcplib.NewToolResultError("fields must be an object"), nil
	}
	sum, err := h.svc.PatchFrontMatter(ctx, domain.NoteRef{Scope: scope, Path: path}, fields, req.GetString("if_match", ""))
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
	scope, _ := req.RequireString("scope")
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	newPath, err := req.RequireString("new_path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	sum, err := h.svc.Move(ctx, domain.NoteRef{Scope: scope, Path: path}, newPath, req.GetString("if_match", ""))
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
	scope, _ := req.RequireString("scope")
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	// Derive archives path: replace first segment with "archives".
	newPath, err := toArchivePath(path)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	sum, err := h.svc.Move(ctx, domain.NoteRef{Scope: scope, Path: path}, newPath, req.GetString("if_match", ""))
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
	scope, _ := req.RequireString("scope")
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	soft := req.GetBool("soft", true)
	err = h.svc.Delete(ctx, domain.NoteRef{Scope: scope, Path: path}, soft)
	if err != nil {
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
		mcplib.WithString("sort", mcplib.Enum("updated", "created", "title"), mcplib.Description("Sort field")),
		mcplib.WithBoolean("desc", mcplib.Description("Sort descending")),
		mcplib.WithNumber("limit", mcplib.Description("Max results (1-100, default 20)")),
		mcplib.WithNumber("offset", mcplib.Description("Pagination offset")),
	)
}

func (h *handlers) notesList(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	f := domain.Filter{
		AllowedScopes: allowedScopes,
		Status:        req.GetString("status", ""),
		Area:          req.GetString("area", ""),
		Project:       req.GetString("project", ""),
		Tags:          req.GetStringSlice("tags", nil),
	}
	for _, c := range req.GetStringSlice("categories", nil) {
		f.Categories = append(f.Categories, domain.Category(c))
	}
	sort := domain.SortField(req.GetString("sort", string(domain.SortByUpdated)))
	limit := req.GetInt("limit", 20)
	offset := req.GetInt("offset", 0)

	result, err := h.svc.Query(ctx, domain.QueryRequest{
		Filter: f,
		Sort:   sort,
		Desc:   req.GetBool("desc", false),
		Limit:  limit,
		Offset: offset,
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
	limit := req.GetInt("limit", 10)
	results, err := h.svc.Search(ctx, text, domain.Filter{AllowedScopes: allowedScopes}, limit)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(results)
}

func toolVaultStats() mcplib.Tool {
	return mcplib.NewTool("vault_stats",
		mcplib.WithDescription("Return aggregate note counts by PARA category."),
	)
}

func (h *handlers) vaultStats(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	stats, err := h.vault.Stats(ctx)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(stats)
}

// --- helpers ---

func jsonResult(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(b)), nil
}

func toolErr(err error) *mcplib.CallToolResult {
	// Surface sentinel errors with structured codes.
	var msg string
	switch {
	case errors.Is(err, domain.ErrNotFound):
		msg = "not_found: " + err.Error()
	case errors.Is(err, domain.ErrConflict):
		msg = "conflict: " + err.Error()
	default:
		msg = err.Error()
	}
	return mcplib.NewToolResultError(msg)
}

// toArchivePath replaces the first path segment with "archives".
func toArchivePath(path string) (string, error) {
	idx := -1
	for i, c := range path {
		if c == '/' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("path has no directory segment: %q", path)
	}
	return "archives" + path[idx:], nil
}
