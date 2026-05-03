package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/ctxutil"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/waitforindex"
	"github.com/whiskeyjimbo/para-mcp/internal/server/audit"
	"github.com/whiskeyjimbo/para-mcp/internal/server/auth"
	"github.com/whiskeyjimbo/para-mcp/internal/server/rbac"
)

const (
	defaultListLimit   = 20
	defaultSearchLimit = 10
	maxListOffset      = 500
)

type handlers struct {
	svc                      ports.NoteService
	scopes                   ports.ScopeResolver
	events                   *EventBus
	auditSearcher            audit.Searcher
	rbacRegistry             *rbac.Registry
	exposeAdminTools         bool
	requirePromotionApproval bool
	semanticEnricher         ports.SemanticEnricher   // optional; nil disables semantic enrichment
	indexStateProvider       ports.IndexStateProvider // optional; nil disables wait_for_index
}

// requireRole returns a permission_denied error result when the caller does not
// hold at least minRole on scope. Returns nil when no RBAC registry is set
// (personal mode) or when there is no caller in context.
func (h *handlers) requireRole(ctx context.Context, scope domain.ScopeID, minRole rbac.Role) *mcplib.CallToolResult {
	if h.rbacRegistry == nil {
		return nil
	}
	caller, ok := auth.CallerFrom(ctx)
	if !ok {
		return nil
	}
	if !h.rbacRegistry.HasRole(caller, scope, minRole) {
		return mcplib.NewToolResultError("permission_denied: insufficient role on scope " + scope)
	}
	return nil
}

func (h *handlers) publishChange(eventType string, ref domain.NoteRef) {
	if h.events != nil {
		h.events.Publish(NoteEvent{Type: eventType, Scope: ref.Scope, Path: ref.Path})
	}
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
		return toolErr(ctx, err), nil
	}
	return jsonResult(note)
}

func (h *handlers) noteCreate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	// Enforce contributor-minimum on the local vault scope.
	if scopes := h.svc.ListScopes(ctx); len(scopes) > 0 {
		if denied := h.requireRole(ctx, scopes[0].Scope, rbac.Contributor); denied != nil {
			return denied, nil
		}
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
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", domain.NoteRef{Scope: res.Summary.Ref.Scope, Path: res.Summary.Ref.Path})
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
}

func (h *handlers) noteUpdateBody(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	if denied := h.requireRole(ctx, ref.Scope, rbac.Contributor); denied != nil {
		return denied, nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	res, err := h.svc.UpdateBody(ctx, ref, body, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", ref)
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
}

func (h *handlers) notePatchFrontMatter(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	if denied := h.requireRole(ctx, ref.Scope, rbac.Contributor); denied != nil {
		return denied, nil
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
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", ref)
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
}

func (h *handlers) noteReplace(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	if denied := h.requireRole(ctx, ref.Scope, rbac.Contributor); denied != nil {
		return denied, nil
	}
	body, err := req.RequireString("body")
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
	res, err := h.svc.Replace(ctx, ref, fields, body, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", ref)
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
}

func (h *handlers) noteMove(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	if denied := h.requireRole(ctx, ref.Scope, rbac.Contributor); denied != nil {
		return denied, nil
	}
	newPath, err := req.RequireString("new_path")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	res, err := h.svc.Move(ctx, ref, newPath, req.GetString("if_match", ""))
	if err != nil {
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", ref)
	h.publishChange("note_changed", domain.NoteRef{Scope: ref.Scope, Path: newPath})
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
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
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", ref)
	h.publishChange("note_changed", domain.NoteRef{Scope: ref.Scope, Path: newPath})
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
}

func (h *handlers) noteDelete(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	if denied := h.requireRole(ctx, ref.Scope, rbac.Contributor); denied != nil {
		return denied, nil
	}
	if err := h.svc.Delete(ctx, ref, req.GetBool("soft", true), req.GetString("if_match", "")); err != nil {
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_deleted", ref)
	return mcplib.NewToolResultText("deleted"), nil
}

func (h *handlers) notePromote(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	toScope, err := req.RequireString("to_scope")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	// Promote requires Lead on the destination scope.
	if denied := h.requireRole(ctx, domain.ScopeID(toScope), rbac.Lead); denied != nil {
		return denied, nil
	}
	// When require_promotion_approval is enabled, short-circuit and return a
	// pending_approval status instead of executing. Workflow approval mechanism
	// is deferred (ADR-0006); this ships the flag infrastructure only.
	if h.requirePromotionApproval {
		return jsonResult(map[string]string{"status": "pending_approval"})
	}
	in := domain.PromoteInput{
		Ref:            ref,
		ToScope:        toScope,
		IfMatch:        req.GetString("if_match", ""),
		KeepSource:     req.GetBool("keep_source", false),
		OnConflict:     domain.ConflictStrategy(req.GetString("on_conflict", "error")),
		IdempotencyKey: req.GetString("idempotency_key", ""),
	}
	res, err := h.svc.Promote(ctx, in)
	if err != nil {
		return toolErr(ctx, err), nil
	}
	h.publishChange("note_changed", domain.NoteRef{Scope: in.ToScope, Path: in.Ref.Path})
	if !in.KeepSource {
		h.publishChange("note_changed", in.Ref)
	}
	return jsonResult(flatMutation(ctx, res, h.semanticEnricher))
}

func (h *handlers) notesList(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	offset := req.GetInt("offset", 0)
	if offset > maxListOffset {
		return mcplib.NewToolResultError(fmt.Sprintf("offset %d exceeds maximum %d; use cursor for deep pagination", offset, maxListOffset)), nil
	}
	cats := parseCategorySlice(req.GetStringSlice("categories", nil))
	result, err := h.svc.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryFilter(domain.NewFilter(
			domain.WithStatus(req.GetString("status", "")),
			domain.WithArea(req.GetString("area", "")),
			domain.WithProject(req.GetString("project", "")),
			domain.WithTags(req.GetStringSlice("tags", nil)...),
			domain.WithCategories(cats...),
		)),
		domain.WithQueryAllowedScopes(h.scopes.Scopes(ctx)),
		domain.WithQuerySort(domain.SortField(req.GetString("sort", string(domain.SortByUpdated))), req.GetBool("desc", false)),
		domain.WithQueryPagination(req.GetInt("limit", defaultListLimit), offset),
		domain.WithQueryCursor(req.GetString("cursor", "")),
	))
	if err != nil {
		return toolErr(ctx, err), nil
	}
	return jsonResult(result)
}

func (h *handlers) notesSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", defaultSearchLimit)
	filter := domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)}

	modeStr := req.GetString("mode", "")
	mode := domain.SearchMode(modeStr)
	explicit := modeStr != ""
	if !explicit {
		if h.svc.SemanticCapable() {
			mode = domain.SearchModeHybrid
		} else {
			mode = domain.SearchModeLexical
		}
	}

	var results []domain.RankedNote
	switch mode {
	case domain.SearchModeLexical:
		results, err = h.svc.Search(ctx, text, filter, limit)
	case domain.SearchModeSemantic:
		results, err = h.svc.SemanticSearch(ctx, text, filter, domain.SemanticSearchOptions{Limit: limit})
	case domain.SearchModeHybrid:
		if explicit && !h.svc.SemanticCapable() {
			return toolErr(ctx, domain.ErrCapabilityUnavailable), nil
		}
		results, err = h.svc.HybridSearch(ctx, text, filter, domain.HybridSearchOptions{Limit: limit})
	default:
		return mcplib.NewToolResultError("invalid_argument: mode must be lexical, semantic, or hybrid"), nil
	}
	if err != nil {
		return toolErr(ctx, err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
}

type waitForIndexResp struct {
	State     domain.IndexState `json:"state"`
	Explainer string            `json:"explainer,omitempty"`
	TimedOut  bool              `json:"timed_out,omitempty"`
	Cancelled bool              `json:"cancelled,omitempty"`
}

func (h *handlers) waitForIndex(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if h.indexStateProvider == nil {
		return toolErr(ctx, domain.ErrCapabilityUnavailable), nil
	}
	ref, errResult := requireNoteRef(req)
	if errResult != nil {
		return errResult, nil
	}
	note, err := h.svc.Get(ctx, ref)
	if err != nil {
		return toolErr(ctx, err), nil
	}
	noteID := domain.GetNoteID(note.FrontMatter)
	if noteID == "" {
		return mcplib.NewToolResultError("not_found: note has no NoteID; cannot poll index state"), nil
	}
	timeoutMs := req.GetInt("index_timeout_ms", 0)
	cfg := waitforindex.DefaultConfig()
	if timeoutMs > 0 {
		cfg.Timeout = waitforindex.ClampTimeout(time.Duration(timeoutMs) * time.Millisecond)
	}
	res := waitforindex.Wait(ctx, noteID, h.indexStateProvider.IndexState, cfg)
	return jsonResult(waitForIndexResp{
		State:     res.State,
		Explainer: res.Explainer,
		TimedOut:  res.TimedOut,
		Cancelled: res.Cancelled,
	})
}

func (h *handlers) notesHybridSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	filter := domain.AuthFilter{
		Filter: domain.NewFilter(
			domain.WithScopes(parseScopeSlice(req.GetStringSlice("scopes", nil))...),
			domain.WithCategories(parseCategorySlice(req.GetStringSlice("categories", nil))...),
		),
		AllowedScopes: h.scopes.Scopes(ctx),
	}
	opts := domain.HybridSearchOptions{Limit: req.GetInt("limit", defaultSearchLimit)}
	results, err := h.svc.HybridSearch(ctx, query, filter, opts)
	if err != nil {
		return toolErr(ctx, err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
}

func (h *handlers) notesSemanticSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	bodyMode := domain.BodyMode(req.GetString("body", string(domain.BodyNever)))
	switch bodyMode {
	case domain.BodyNever, domain.BodyOnDemand:
	default:
		return mcplib.NewToolResultError("invalid_argument: body must be 'never' or 'on_demand'"), nil
	}
	threshold := req.GetFloat("threshold", 0)
	if threshold < 0 || threshold > 1 {
		return mcplib.NewToolResultError("invalid_argument: threshold must be in [0,1]"), nil
	}

	filter := domain.AuthFilter{
		Filter: domain.NewFilter(
			domain.WithScopes(parseScopeSlice(req.GetStringSlice("scopes", nil))...),
			domain.WithCategories(parseCategorySlice(req.GetStringSlice("categories", nil))...),
		),
		AllowedScopes: h.scopes.Scopes(ctx),
	}
	opts := domain.SemanticSearchOptions{
		Limit:     req.GetInt("limit", defaultSearchLimit),
		Threshold: threshold,
		BodyMode:  bodyMode,
	}
	results, err := h.svc.SemanticSearch(ctx, query, filter, opts)
	if err != nil {
		return toolErr(ctx, err), nil
	}
	if results == nil {
		results = []domain.RankedNote{}
	}
	return jsonResult(results)
}

func (h *handlers) vaultStats(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	stats, err := h.svc.Stats(ctx)
	if err != nil {
		return toolErr(ctx, err), nil
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
		return toolErr(ctx, err), nil
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
		return toolErr(ctx, err), nil
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
	cats := parseCategorySlice(req.GetStringSlice("categories", nil))
	result, err := h.svc.Stale(ctx, days, cats, req.GetString("status", ""), req.GetInt("limit", defaultListLimit), domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)})
	if err != nil {
		return toolErr(ctx, err), nil
	}
	return jsonResult(result)
}

func (h *handlers) vaultHealth(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	health, err := h.svc.Health(ctx)
	if err != nil {
		return toolErr(ctx, err), nil
	}
	return jsonResult(health)
}

func (h *handlers) vaultRescan(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if err := h.svc.Rescan(ctx); err != nil {
		return toolErr(ctx, err), nil
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
	result, err := h.svc.CreateBatch(ctx, inputs, domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)})
	if err != nil {
		return toolErr(ctx, err), nil
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
	result, err := h.svc.UpdateBodyBatch(ctx, inputs, domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)})
	if err != nil {
		return toolErr(ctx, err), nil
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
	result, err := h.svc.PatchFrontMatterBatch(ctx, inputs, domain.AuthFilter{AllowedScopes: h.scopes.Scopes(ctx)})
	if err != nil {
		return toolErr(ctx, err), nil
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
// all summary fields at the top level alongside the ETag concurrency token and
// optional semantic fields.
type mutationResult struct {
	domain.NoteSummary
	ETag                string `json:"ETag"`
	Generated           bool   `json:"generated,omitempty"`
	Warning             string `json:"_warning,omitempty"`
	IndexStateExplainer string `json:"index_state_explainer,omitempty"`
}

const generatedWarning = "This response includes AI-generated content (summary, entities, suggested tags). Review before relying on it."

func flatMutation(ctx context.Context, r domain.MutationResult, enr ports.SemanticEnricher) mutationResult {
	sum := r.Summary
	mr := mutationResult{NoteSummary: sum, ETag: r.ETag}
	if enr == nil {
		return mr
	}
	enr.Enrich(ctx, r.Summary.Ref, &mr.NoteSummary)
	if mr.Derived != nil {
		mr.Generated = true
		mr.Warning = generatedWarning
	}
	if mr.IndexState != domain.IndexStateIndexed && mr.IndexState != "" {
		mr.IndexStateExplainer = mr.IndexState.Explain()
	}
	return mr
}

func jsonResult(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(b)), nil
}

func parseCategorySlice(raw []string) []domain.Category {
	cats := make([]domain.Category, 0, len(raw))
	for _, c := range raw {
		cats = append(cats, domain.Category(c))
	}
	return cats
}

func parseScopeSlice(raw []string) []domain.ScopeID {
	out := make([]domain.ScopeID, 0, len(raw))
	for _, s := range raw {
		out = append(out, domain.ScopeID(s))
	}
	return out
}

var errPrefixes = []struct {
	sentinel error
	prefix   string
}{
	{domain.ErrNotFound, "not_found"},
	{domain.ErrInvalidPath, "invalid_path"},
	{domain.ErrInvalidFrontMatter, "invalid_frontmatter"},
	{domain.ErrScopeForbidden, "scope_forbidden"},
	{domain.ErrUnavailable, "unavailable"},
	{domain.ErrInvalidCursor, "invalid_argument"},
	{domain.ErrCapabilityUnavailable, "capability_unavailable"},
}

// toolErr converts a domain error to an MCP tool error result.
// For conflict errors, use toolErr(ctx, err) to include request_id.
func toolErr(ctx context.Context, err error) *mcplib.CallToolResult {
	if errors.Is(err, domain.ErrConflict) {
		type detail struct {
			RequestID string `json:"request_id,omitempty"`
		}
		type conflictResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Details detail `json:"details"`
		}
		reqID := ctxutil.RequestIDFromContext(ctx)
		b, _ := json.Marshal(conflictResp{
			Error:   "conflict",
			Message: err.Error(),
			Details: detail{RequestID: reqID},
		})
		return mcplib.NewToolResultError(string(b))
	}
	for _, e := range errPrefixes {
		if errors.Is(err, e.sentinel) {
			return mcplib.NewToolResultError(e.prefix + ": " + err.Error())
		}
	}
	return mcplib.NewToolResultError(err.Error())
}

const errPermissionDenied = "permission_denied: audit_search requires admin role and expose_admin_tools enabled"

func (h *handlers) auditSearch(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	// Gate: expose_admin_tools flag must be true.
	if !h.exposeAdminTools {
		return mcplib.NewToolResultError(errPermissionDenied), nil
	}

	scope, err := req.RequireString("scope")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	// Gate: caller must hold Admin role on the requested scope.
	caller, ok := auth.CallerFrom(ctx)
	if !ok || h.rbacRegistry == nil || !h.rbacRegistry.HasRole(caller, domain.ScopeID(scope), rbac.Admin) {
		return mcplib.NewToolResultError(errPermissionDenied), nil
	}

	if h.auditSearcher == nil {
		return mcplib.NewToolResultError("audit search not available"), nil
	}

	f := audit.SearchFilter{
		Actor:   req.GetString("actor", ""),
		Action:  req.GetString("action", ""),
		Outcome: req.GetString("outcome", ""),
		Scope:   scope,
		Limit:   int(req.GetFloat("limit", 0)),
		Offset:  int(req.GetFloat("offset", 0)),
	}
	if s := req.GetString("since", ""); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return mcplib.NewToolResultError("invalid since: " + err.Error()), nil
		}
		f.Since = t
	}
	if s := req.GetString("until", ""); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return mcplib.NewToolResultError("invalid until: " + err.Error()), nil
		}
		f.Until = t
	}

	rows, err := h.auditSearcher.Search(ctx, f)
	if err != nil {
		return mcplib.NewToolResultError("audit search: " + err.Error()), nil
	}
	return jsonResult(rows)
}
