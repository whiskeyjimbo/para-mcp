package remotevault

import (
	"context"
	"fmt"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
)

// RemoteVault implements ports.Vault against a remote paras MCP server.
// It rewrites the remote's canonical scope name to the local scope alias
// on every inbound response, so callers always see the local ScopeID.
//
// Write operations (Create, UpdateBody, etc.) are not supported in Phase 3
// and return domain.ErrScopeForbidden.
type RemoteVault struct {
	// localScope is the local alias callers and the registry use.
	localScope domain.ScopeID
	// canonicalRemote is the scope name the remote server uses.
	canonicalRemote string
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
	args := queryRequestToArgs(v.canonicalRemote, q)
	var result domain.QueryResult
	if err := v.conn.call(ctx, "notes_list", args, &result); err != nil {
		return domain.QueryResult{}, translateErr(err)
	}
	v.rewriteScopes(result.Notes)
	return result, nil
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

// --- Write operations: not supported in Phase 3 ---

func (v *RemoteVault) Create(_ context.Context, _ domain.CreateInput) (domain.MutationResult, error) {
	return domain.MutationResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) UpdateBody(_ context.Context, _, _, _ string) (domain.MutationResult, error) {
	return domain.MutationResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) PatchFrontMatter(_ context.Context, _ string, _ map[string]any, _ string) (domain.MutationResult, error) {
	return domain.MutationResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) Move(_ context.Context, _, _, _ string) (domain.MutationResult, error) {
	return domain.MutationResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) Delete(_ context.Context, _ string, _ bool) error {
	return fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) CreateBatch(_ context.Context, _ []domain.CreateInput) (domain.BatchResult, error) {
	return domain.BatchResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) UpdateBodyBatch(_ context.Context, _ []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return domain.BatchResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
}

func (v *RemoteVault) PatchFrontMatterBatch(_ context.Context, _ []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return domain.BatchResult{}, fmt.Errorf("%w: remote vault %q is read-only in Phase 3", domain.ErrScopeForbidden, v.localScope)
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

// translateErr maps the remote error prefix (e.g. "not_found: ...") injected
// by the remote's toolErr function back to a domain sentinel error.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// Strip the "remotevault: <tool>: remote error: " wrapper to get the prefix.
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
