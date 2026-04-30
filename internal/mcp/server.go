// Package mcp wires LocalVault to the MCP tool surface via stdio transport.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/whiskeyjimbo/paras/internal/domain"
	"github.com/whiskeyjimbo/paras/internal/vault"
)

// ScopesFunc resolves the permitted scopes for a request.
// Phase 1: returns a hard-coded single-vault slice.
// Phase 3: replaced with an RBAC resolver that reads caller identity
// from ctx and returns only the scopes that caller may access.
type ScopesFunc func(ctx context.Context) []domain.ScopeID

// personalOnly is the Phase 1 resolver: always permit the personal vault.
func personalOnly(_ context.Context) []domain.ScopeID {
	return []domain.ScopeID{"personal"}
}

// Build constructs and returns an MCPServer wired to svc.
// scopesFn resolves permitted scopes per request; pass nil to use the
// default single-vault personal resolver.
func Build(svc *vault.NoteService, scopesFn ScopesFunc) *mcpserver.MCPServer {
	if scopesFn == nil {
		scopesFn = personalOnly
	}
	s := mcpserver.NewMCPServer(
		"paras",
		"0.1.0",
		mcpserver.WithToolCapabilities(true),
	)

	h := &handlers{svc: svc, scopes: scopesFn}

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
	svc    *vault.NoteService
	scopes ScopesFunc
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
	newPath, err := toArchivePath(ref.Path)
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

func toArchivePath(path string) (string, error) {
	_, rest, ok := strings.Cut(path, "/")
	if !ok {
		return "", fmt.Errorf("path has no directory segment: %q", path)
	}
	return "archives/" + rest, nil
}
