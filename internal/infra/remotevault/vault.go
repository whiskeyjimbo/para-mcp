package remotevault

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

// remoteMutation is the flat JSON shape the remote MCP server returns for write operations.
// The MCP handler embeds NoteSummary at the top level rather than nesting it under "Summary".
type remoteMutation struct {
	domain.NoteSummary
	ETag string `json:"ETag"`
}

func (r remoteMutation) toDomain() domain.MutationResult {
	return domain.MutationResult{Summary: r.NoteSummary, ETag: r.ETag}
}

// RemoteVault implements ports.Vault against a remote paras MCP server.
// It rewrites the remote's canonical scope name to the local scope alias
// on every inbound response, so callers always see the local ScopeID.
type RemoteVault struct {
	// localScope is the local alias callers and the registry use.
	localScope domain.ScopeID
	// canonicalRemote is the scope name the remote server uses.
	canonicalRemote string
	baseURL         string
	conn            *mcpConn
	caps            domain.Capabilities
	summaries       *summaryCache
	bodies          *bodyCache
}

var _ ports.Vault = (*RemoteVault)(nil)

// Config holds the parameters for connecting to a remote vault.
type Config struct {
	// LocalScope is the local scope alias (e.g. "team-platform").
	LocalScope domain.ScopeID
	// CanonicalRemote is the scope name on the remote server (e.g. "team-infra").
	// If empty, LocalScope is used.
	CanonicalRemote string
	// BaseURL is the remote server's MCP HTTP endpoint.
	BaseURL string
}

// New creates a RemoteVault and performs the MCP Initialize handshake.
func New(ctx context.Context, cfg Config) (*RemoteVault, error) {
	conn, err := newConn(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if err := conn.initialize(ctx); err != nil {
		return nil, fmt.Errorf("remotevault: initialize %q: %w", cfg.BaseURL, err)
	}
	canonical := cfg.CanonicalRemote
	if canonical == "" {
		canonical = cfg.LocalScope
	}
	v := &RemoteVault{
		localScope:      cfg.LocalScope,
		canonicalRemote: canonical,
		baseURL:         cfg.BaseURL,
		conn:            conn,
		summaries:       newSummaryCache(),
		bodies:          newBodyCache(),
	}
	// Fetch capabilities from the remote scope listing.
	if err := v.fetchCapabilities(ctx); err != nil {
		// Non-fatal: proceed with zero Capabilities; ops will fail gracefully.
		_ = err
	}
	return v, nil
}

// fetchCapabilities calls vault_list_scopes on the remote and finds our scope.
func (v *RemoteVault) fetchCapabilities(ctx context.Context) error {
	var scopes []domain.ScopeInfo
	if err := v.conn.call(ctx, "vault_list_scopes", nil, &scopes); err != nil {
		return err
	}
	for _, s := range scopes {
		if s.Scope == v.canonicalRemote {
			v.caps = s.Capabilities
			return nil
		}
	}
	return nil
}

func (v *RemoteVault) Scope() domain.ScopeID             { return v.localScope }
func (v *RemoteVault) Capabilities() domain.Capabilities { return v.caps }

func (v *RemoteVault) Close() error { return nil }

func (v *RemoteVault) Get(ctx context.Context, path string) (domain.Note, error) {
	if n, ok := v.bodies.get(path); ok {
		return n, nil
	}
	args := map[string]any{
		"scope": v.canonicalRemote,
		"path":  path,
	}
	var n domain.Note
	if err := v.conn.call(ctx, "note_get", args, &n); err != nil {
		return domain.Note{}, translateErr(err)
	}
	n.Ref.Scope = v.localScope
	v.bodies.set(path, n)
	return n, nil
}

func (v *RemoteVault) Query(ctx context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	key := queryCacheKey(q)
	if cached, ok := v.summaries.get(key); ok {
		return cached, nil
	}
	args := queryRequestToArgs(v.canonicalRemote, q)
	var result domain.QueryResult
	if err := v.conn.call(ctx, "notes_list", args, &result); err != nil {
		return domain.QueryResult{}, translateErr(err)
	}
	v.rewriteScopes(result.Notes)
	v.summaries.set(key, result)
	return result, nil
}

func queryCacheKey(q domain.QueryRequest) string {
	b, _ := json.Marshal(q)
	return string(b)
}

func (v *RemoteVault) Search(ctx context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	args := map[string]any{
		"text":  text,
		"limit": limit,
	}
	var results []domain.RankedNote
	if err := v.conn.call(ctx, "notes_search", args, &results); err != nil {
		return nil, translateErr(err)
	}
	for i := range results {
		results[i].Summary.Ref.Scope = v.localScope
	}
	return results, nil
}

func (v *RemoteVault) Backlinks(ctx context.Context, ref domain.NoteRef, includeAssets bool, _ domain.Filter) ([]domain.BacklinkEntry, error) {
	args := map[string]any{
		"scope":          v.canonicalRemote,
		"path":           ref.Path,
		"include_assets": includeAssets,
	}
	var entries []domain.BacklinkEntry
	if err := v.conn.call(ctx, "notes_backlinks", args, &entries); err != nil {
		return nil, translateErr(err)
	}
	for i := range entries {
		entries[i].Summary.Ref.Scope = v.localScope
	}
	return entries, nil
}

func (v *RemoteVault) Stats(ctx context.Context) (domain.VaultStats, error) {
	var stats domain.VaultStats
	if err := v.conn.call(ctx, "vault_stats", nil, &stats); err != nil {
		return domain.VaultStats{}, translateErr(err)
	}
	return stats, nil
}

func (v *RemoteVault) Health(ctx context.Context) (domain.VaultHealth, error) {
	var health domain.VaultHealth
	if err := v.conn.call(ctx, "vault_health", nil, &health); err != nil {
		return domain.VaultHealth{}, translateErr(err)
	}
	return health, nil
}

func (v *RemoteVault) Rescan(ctx context.Context) error {
	return v.conn.call(ctx, "vault_rescan", nil, nil)
}

// --- Write operations ---

func (v *RemoteVault) Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error) {
	args := map[string]any{
		"path":    in.Path,
		"title":   in.FrontMatter.Title,
		"status":  in.FrontMatter.Status,
		"area":    in.FrontMatter.Area,
		"project": in.FrontMatter.Project,
		"tags":    in.FrontMatter.Tags,
		"body":    in.Body,
	}
	var raw remoteMutation
	if err := v.conn.call(ctx, "note_create", args, &raw); err != nil {
		return domain.MutationResult{}, translateErr(err)
	}
	res := raw.toDomain()
	res.Summary.Ref.Scope = v.localScope
	v.summaries.invalidate()
	return res, nil
}

func (v *RemoteVault) UpdateBody(ctx context.Context, path, body, ifMatch string) (domain.MutationResult, error) {
	args := map[string]any{
		"scope":    v.canonicalRemote,
		"path":     path,
		"body":     body,
		"if_match": ifMatch,
	}
	var raw remoteMutation
	if err := v.conn.call(ctx, "note_update_body", args, &raw); err != nil {
		return domain.MutationResult{}, translateErr(err)
	}
	res := raw.toDomain()
	res.Summary.Ref.Scope = v.localScope
	v.bodies.invalidate(path)
	v.summaries.invalidate()
	return res, nil
}

func (v *RemoteVault) PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.MutationResult, error) {
	args := map[string]any{
		"scope":    v.canonicalRemote,
		"path":     path,
		"fields":   fields,
		"if_match": ifMatch,
	}
	var raw remoteMutation
	if err := v.conn.call(ctx, "note_patch_frontmatter", args, &raw); err != nil {
		return domain.MutationResult{}, translateErr(err)
	}
	res := raw.toDomain()
	res.Summary.Ref.Scope = v.localScope
	v.bodies.invalidate(path)
	v.summaries.invalidate()
	return res, nil
}

func (v *RemoteVault) Move(ctx context.Context, path, newPath, ifMatch string) (domain.MutationResult, error) {
	args := map[string]any{
		"scope":    v.canonicalRemote,
		"path":     path,
		"new_path": newPath,
		"if_match": ifMatch,
	}
	var raw remoteMutation
	if err := v.conn.call(ctx, "note_move", args, &raw); err != nil {
		return domain.MutationResult{}, translateErr(err)
	}
	res := raw.toDomain()
	res.Summary.Ref.Scope = v.localScope
	v.bodies.invalidate(path)
	v.bodies.invalidate(newPath)
	v.summaries.invalidate()
	return res, nil
}

func (v *RemoteVault) Delete(ctx context.Context, path string, soft bool, ifMatch string) error {
	args := map[string]any{
		"scope":    v.canonicalRemote,
		"path":     path,
		"soft":     soft,
		"if_match": ifMatch,
	}
	if err := v.conn.call(ctx, "note_delete", args, nil); err != nil {
		return translateErr(err)
	}
	v.bodies.invalidate(path)
	v.summaries.invalidate()
	return nil
}

func (v *RemoteVault) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	notes := make([]map[string]any, len(inputs))
	for i, in := range inputs {
		notes[i] = map[string]any{
			"path":    in.Path,
			"title":   in.FrontMatter.Title,
			"status":  in.FrontMatter.Status,
			"area":    in.FrontMatter.Area,
			"project": in.FrontMatter.Project,
			"tags":    in.FrontMatter.Tags,
			"body":    in.Body,
		}
	}
	args := map[string]any{"notes": notes}
	var res domain.BatchResult
	if err := v.conn.call(ctx, "notes_create_batch", args, &res); err != nil {
		return domain.BatchResult{}, translateErr(err)
	}
	v.summaries.invalidate()
	return res, nil
}

func (v *RemoteVault) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	notes := make([]map[string]any, len(items))
	for i, it := range items {
		notes[i] = map[string]any{
			"scope":    v.canonicalRemote,
			"path":     it.Path,
			"body":     it.Body,
			"if_match": it.IfMatch,
		}
	}
	args := map[string]any{"notes": notes}
	var res domain.BatchResult
	if err := v.conn.call(ctx, "notes_update_batch", args, &res); err != nil {
		return domain.BatchResult{}, translateErr(err)
	}
	v.summaries.invalidate()
	return res, nil
}

func (v *RemoteVault) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	notes := make([]map[string]any, len(items))
	for i, it := range items {
		notes[i] = map[string]any{
			"scope":    v.canonicalRemote,
			"path":     it.Path,
			"fields":   it.Fields,
			"if_match": it.IfMatch,
		}
	}
	args := map[string]any{"notes": notes}
	var res domain.BatchResult
	if err := v.conn.call(ctx, "notes_patch_frontmatter_batch", args, &res); err != nil {
		return domain.BatchResult{}, translateErr(err)
	}
	v.summaries.invalidate()
	return res, nil
}

// --- Helpers ---

// rewriteScopes replaces the remote canonical scope name with the local alias.
func (v *RemoteVault) rewriteScopes(notes []domain.NoteSummary) {
	for i := range notes {
		notes[i].Ref.Scope = v.localScope
	}
}

// queryRequestToArgs converts a QueryRequest to an MCP tool argument map.
func queryRequestToArgs(scope domain.ScopeID, q domain.QueryRequest) map[string]any {
	args := map[string]any{
		"scope":  scope,
		"limit":  q.Limit,
		"offset": q.Offset,
	}
	if q.Sort != "" {
		args["sort"] = string(q.Sort)
	}
	if q.Desc {
		args["desc"] = true
	}
	if q.Filter.Status != "" {
		args["status"] = q.Filter.Status
	}
	if q.Filter.Area != "" {
		args["area"] = q.Filter.Area
	}
	if q.Filter.Project != "" {
		args["project"] = q.Filter.Project
	}
	if len(q.Filter.Tags) > 0 {
		args["tags"] = q.Filter.Tags
	}
	if len(q.Filter.Categories) > 0 {
		cats := make([]string, len(q.Filter.Categories))
		for i, c := range q.Filter.Categories {
			cats[i] = string(c)
		}
		args["categories"] = cats
	}
	if q.Cursor != "" {
		args["cursor"] = q.Cursor
	}
	return args
}

// translateErr maps the remote error back to a domain sentinel error.
// The remote emits two formats: plain "prefix: message" for most errors, and
// JSON {"error":"conflict",...} for conflict errors (to carry details.request_id).
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	const marker = "remote error: "
	idx := 0
	for i := 0; i+len(marker) <= len(msg); i++ {
		if msg[i:i+len(marker)] == marker {
			idx = i + len(marker)
			break
		}
	}
	if idx == 0 {
		return err
	}
	tail := msg[idx:]
	// JSON format: {"error":"conflict",...}
	if len(tail) > 0 && tail[0] == '{' {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(tail), &errResp) == nil {
			if sentinel, ok := remoteErrPrefixes[errResp.Error]; ok {
				return fmt.Errorf("%w: %s", sentinel, msg)
			}
		}
		return err
	}
	// Plain prefix format: "prefix: message"
	for prefix, sentinel := range remoteErrPrefixes {
		if len(tail) >= len(prefix) && tail[:len(prefix)] == prefix {
			return fmt.Errorf("%w: %s", sentinel, msg)
		}
	}
	return err
}

var remoteErrPrefixes = map[string]error{
	"not_found":           domain.ErrNotFound,
	"conflict":            domain.ErrConflict,
	"invalid_path":        domain.ErrInvalidPath,
	"invalid_frontmatter": domain.ErrInvalidFrontMatter,
	"scope_forbidden":     domain.ErrScopeForbidden,
}
