package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

const (
	defaultListLimit   = 20
	defaultSearchLimit = 10
)

type handlers struct {
	svc    ports.NoteService
	scopes ports.ScopeResolver
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

func (h *handlers) noteCreate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	in := domain.NewCreateInput(
		path,
		domain.NewFrontMatter(
			req.GetString("title", ""),
			req.GetString("status", ""),
			req.GetString("area", ""),
			req.GetString("project", ""),
			req.GetStringSlice("tags", nil),
		),
		req.GetString("body", ""),
	)
	res, err := h.svc.Create(ctx, in)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(flatMutation(res))
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
	res, err := h.svc.UpdateBody(ctx, ref, body, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(flatMutation(res))
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
	res, err := h.svc.PatchFrontMatter(ctx, ref, fields, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(flatMutation(res))
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
	res, err := h.svc.Move(ctx, ref, newPath, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(flatMutation(res))
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
	res, err := h.svc.Move(ctx, ref, newPath, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(flatMutation(res))
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

func (h *handlers) notesList(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	cats := make([]domain.Category, 0, len(req.GetStringSlice("categories", nil)))
	for _, c := range req.GetStringSlice("categories", nil) {
		cats = append(cats, domain.Category(c))
	}
	result, err := h.svc.Query(ctx, domain.QueryRequest{
		Filter: domain.NewFilter(
			domain.WithStatus(req.GetString("status", "")),
			domain.WithArea(req.GetString("area", "")),
			domain.WithProject(req.GetString("project", "")),
			domain.WithTags(req.GetStringSlice("tags", nil)...),
			domain.WithCategories(cats...),
		),
		AllowedScopes: h.scopes.Scopes(ctx),
		Sort:          domain.SortField(req.GetString("sort", string(domain.SortByUpdated))),
		Desc:          req.GetBool("desc", false),
		Limit:         req.GetInt("limit", defaultListLimit),
		Offset:        req.GetInt("offset", 0),
	})
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func (h *handlers) notesSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	results, err := h.svc.Search(ctx, text, domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)}, req.GetInt("limit", defaultSearchLimit))
	if err != nil {
		return toolErr(err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
}

func (h *handlers) vaultStats(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	stats, err := h.svc.Stats(ctx)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(stats)
}

func (h *handlers) notesBacklinks(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	entries, err := h.svc.Backlinks(ctx, ref, req.GetBool("include_assets", false),
		domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)})
	if err != nil {
		return toolErr(err), nil
	}
	if entries == nil {
		entries = []domain.BacklinkEntry{}
	}
	return jsonResult(entries)
}

func (h *handlers) notesRelated(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	results, err := h.svc.Related(ctx, ref, req.GetInt("limit", defaultSearchLimit), domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)})
	if err != nil {
		return toolErr(err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
}

func (h *handlers) notesStale(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	days := req.GetInt("days", 0)
	if days <= 0 {
		return mcplib.NewToolResultError("days must be > 0"), nil
	}
	cats := make([]domain.Category, 0, len(req.GetStringSlice("categories", nil)))
	for _, c := range req.GetStringSlice("categories", nil) {
		cats = append(cats, domain.Category(c))
	}
	result, err := h.svc.Stale(ctx, days, cats, req.GetString("status", ""), req.GetInt("limit", defaultListLimit), h.scopes.Scopes(ctx))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
}

func (h *handlers) vaultHealth(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	health, err := h.svc.Health(ctx)
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(health)
}

func (h *handlers) vaultRescan(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if err := h.svc.Rescan(ctx); err != nil {
		return toolErr(err), nil
	}
	return mcplib.NewToolResultText("rescan complete"), nil
}

func (h *handlers) vaultListScopes(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	return jsonResult(h.svc.ListScopes(ctx))
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
		inputs = append(inputs, domain.NewCreateInput(
			path,
			domain.NewFrontMatter(
				stringVal(obj, "title"),
				stringVal(obj, "status"),
				stringVal(obj, "area"),
				stringVal(obj, "project"),
				stringSliceVal(obj, "tags"),
			),
			stringVal(obj, "body"),
		))
	}
	result, err := h.svc.CreateBatch(ctx, inputs, h.scopes.Scopes(ctx))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
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
	result, err := h.svc.UpdateBodyBatch(ctx, inputs, h.scopes.Scopes(ctx))
	if err != nil {
		return toolErr(err), nil
	}
	return jsonResult(result)
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
	result, err := h.svc.PatchFrontMatterBatch(ctx, inputs, h.scopes.Scopes(ctx))
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

// mutationResult flattens a MutationResult into a single JSON object, keeping
// all summary fields at the top level alongside the ETag concurrency token.
type mutationResult struct {
	domain.NoteSummary
	ETag string `json:"etag"`
}

func flatMutation(r domain.MutationResult) mutationResult {
	return mutationResult{NoteSummary: r.Summary, ETag: r.ETag}
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
