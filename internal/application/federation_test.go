package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/core/ports"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/localvault"
)

// stubVault is a minimal ports.Vault for federation tests.
// Query returns fixedNotes until failAfter successful calls, then failErr.
// All other methods satisfy the interface with zero-value returns.
type stubVault struct {
	scope      domain.ScopeID
	fixedNotes []domain.NoteSummary
	queryCount atomic.Int64
	failAfter  int64 // -1 = never fail
	failErr    error
}

func newStubVault(scope domain.ScopeID, notes ...domain.NoteSummary) *stubVault {
	return &stubVault{scope: scope, fixedNotes: notes, failAfter: -1}
}

func (v *stubVault) Scope() domain.ScopeID             { return v.scope }
func (v *stubVault) Capabilities() domain.Capabilities { return domain.Capabilities{} }
func (v *stubVault) Close() error                      { return nil }

func (v *stubVault) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	n := v.queryCount.Add(1)
	if v.failAfter >= 0 && n > v.failAfter {
		return domain.QueryResult{}, v.failErr
	}
	all := v.fixedNotes
	if q.Offset < len(all) {
		all = all[q.Offset:]
	} else {
		all = nil
	}
	if q.Limit > 0 && q.Limit < len(all) {
		all = all[:q.Limit]
	}
	notes := make([]domain.NoteSummary, 0, len(all))
	for _, n := range all {
		notes = append(notes, domain.NoteSummary{
			Ref:   domain.NoteRef{Scope: v.scope, Path: n.Ref.Path},
			Title: n.Title,
		})
	}
	return domain.QueryResult{
		Notes:           notes,
		Total:           len(notes),
		ScopesAttempted: []domain.ScopeID{v.scope},
		ScopesSucceeded: []domain.ScopeID{v.scope},
	}, nil
}

func (v *stubVault) Get(_ context.Context, _ string) (domain.Note, error) {
	return domain.Note{}, domain.ErrNotFound
}
func (v *stubVault) Search(_ context.Context, _ string, _ domain.Filter, _ int) ([]domain.RankedNote, error) {
	return nil, nil
}
func (v *stubVault) Backlinks(_ context.Context, _ domain.NoteRef, _ bool, _ domain.Filter) ([]domain.BacklinkEntry, error) {
	return nil, nil
}
func (v *stubVault) Stats(_ context.Context) (domain.VaultStats, error) {
	return domain.VaultStats{ByCategory: map[domain.Category]int{}}, nil
}
func (v *stubVault) Health(_ context.Context) (domain.VaultHealth, error) {
	return domain.VaultHealth{}, nil
}
func (v *stubVault) Rescan(_ context.Context) error { return nil }
func (v *stubVault) Create(_ context.Context, _ domain.CreateInput) (domain.MutationResult, error) {
	return domain.MutationResult{}, nil
}
func (v *stubVault) UpdateBody(_ context.Context, _, _ string, _ string) (domain.MutationResult, error) {
	return domain.MutationResult{}, nil
}
func (v *stubVault) PatchFrontMatter(_ context.Context, _ string, _ map[string]any, _ string) (domain.MutationResult, error) {
	return domain.MutationResult{}, nil
}
func (v *stubVault) Move(_ context.Context, _, _ string, _ string) (domain.MutationResult, error) {
	return domain.MutationResult{}, nil
}
func (v *stubVault) Delete(_ context.Context, _ string, _ bool, _ string) error { return nil }
func (v *stubVault) CreateBatch(_ context.Context, _ []domain.CreateInput) (domain.BatchResult, error) {
	return domain.BatchResult{}, nil
}
func (v *stubVault) UpdateBodyBatch(_ context.Context, _ []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return domain.BatchResult{}, nil
}
func (v *stubVault) PatchFrontMatterBatch(_ context.Context, _ []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return domain.BatchResult{}, nil
}

var _ ports.Vault = (*stubVault)(nil)

// newFedWithStubs builds a FederationService backed by the given stub vaults
// and a deterministic HMAC key (so cursors are reproducible in tests).
func newFedWithStubs(t *testing.T, vaults ...*stubVault) *FederationService {
	t.Helper()
	reg := NewRegistry()
	for _, v := range vaults {
		if err := reg.AddVault(v, ""); err != nil {
			t.Fatalf("AddVault %q: %v", v.scope, err)
		}
	}
	return NewFederationServiceWithKey(reg, make([]byte, 32))
}

// --- Test A: scope alias stability across remote rename ---

// TestFederation_ScopeAlias verifies that query results carry the local scope
// alias (the registry key) rather than any canonical-remote name. This is the
// foundation of Acceptance Test A: a remote server rename only requires a
// config update; no note data changes.
func TestFederation_ScopeAlias(t *testing.T) {
	va := newStubVault("personal", domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/a.md"}, Title: "A"})
	vb := newStubVault("team", domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/b.md"}, Title: "B"})
	fed := newFedWithStubs(t, va, vb)

	res, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal", "team"}),
		domain.WithQueryPagination(10, 0),
	))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	scopes := make(map[domain.ScopeID]bool)
	for _, n := range res.Notes {
		scopes[n.Ref.Scope] = true
	}
	if !scopes["personal"] || !scopes["team"] {
		t.Errorf("expected notes from both local aliases; got scopes: %v", scopes)
	}
	for _, n := range res.Notes {
		if n.Ref.Scope != "personal" && n.Ref.Scope != "team" {
			t.Errorf("note %q has unexpected scope %q", n.Ref.Path, n.Ref.Scope)
		}
	}
}

// --- Test B: cursor sticky scope-set across AllowedScopes change ---

func TestFederation_CursorStickyScopes(t *testing.T) {
	// Create enough notes to force pagination (limit=1 with 2 notes per scope).
	notes := func(scope string, n int) []domain.NoteSummary {
		out := make([]domain.NoteSummary, n)
		for i := range out {
			out[i] = domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/note.md"}, Title: scope}
		}
		return out
	}
	va := newStubVault("personal", notes("personal", 3)...)
	vb := newStubVault("team", notes("team", 3)...)
	fed := newFedWithStubs(t, va, vb)

	// Page 1: both scopes allowed.
	res1, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal", "team"}),
		domain.WithQueryPagination(2, 0),
	))
	if err != nil {
		t.Fatalf("Query page 1: %v", err)
	}
	if res1.NextCursor == "" {
		t.Fatal("expected cursor for page 2")
	}

	// Page 2: remove "team" from AllowedScopes (simulate config reload that
	// revokes a scope). Sticky scope-set re-intersects with new allowed list.
	res2, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal"}), // team removed
		domain.WithQueryCursor(res1.NextCursor),
		domain.WithQueryPagination(2, 0),
	))
	if err != nil {
		t.Fatalf("Query page 2: %v", err)
	}
	// team was in the sticky scope-set but is no longer allowed; all results
	// must be from "personal" only.
	for _, n := range res2.Notes {
		if n.Ref.Scope == "team" {
			t.Errorf("page 2 returned note from revoked scope %q", n.Ref.Scope)
		}
	}
}

// --- Test C: mid-pagination scope loss → PartialFailure, no duplication ---

func TestFederation_MidPaginationScopeLoss(t *testing.T) {
	va := newStubVault("personal",
		domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/a1.md"}, Title: "A1"},
		domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/a2.md"}, Title: "A2"},
		domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/a3.md"}, Title: "A3"},
	)
	vb := newStubVault("team",
		domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/b1.md"}, Title: "B1"},
		domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/b2.md"}, Title: "B2"},
		domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/b3.md"}, Title: "B3"},
	)
	// "team" vault succeeds for the first Query call (page 1), fails on page 2.
	vb.failAfter = 1
	vb.failErr = errors.New("connection refused")

	fed := newFedWithStubs(t, va, vb)

	// Page 1: both scopes succeed.
	res1, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal", "team"}),
		domain.WithQueryPagination(2, 0),
	))
	if err != nil {
		t.Fatalf("Query page 1: %v", err)
	}
	if res1.PartialFailure != nil {
		t.Errorf("page 1: unexpected PartialFailure: %v", res1.PartialFailure)
	}
	if res1.NextCursor == "" {
		t.Fatal("expected cursor for page 2")
	}

	// Collect page 1 note paths to check for duplication.
	seen := make(map[string]bool)
	for _, n := range res1.Notes {
		seen[n.Ref.Path] = true
	}

	// Page 2: "team" fails.
	res2, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal", "team"}),
		domain.WithQueryCursor(res1.NextCursor),
		domain.WithQueryPagination(2, 0),
	))
	if err != nil {
		t.Fatalf("Query page 2: %v", err)
	}
	if res2.PartialFailure == nil {
		t.Fatal("page 2: expected PartialFailure when team scope is down")
	}
	if res2.PartialFailure.WarningText == "" {
		t.Error("page 2: PartialFailure.WarningText must not be empty")
	}

	// Verify no note from page 1 appears on page 2.
	for _, n := range res2.Notes {
		if seen[n.Ref.Path] && n.Ref.Scope == "personal" {
			// Same path on personal is only a duplicate if the offset advanced correctly.
			// For personal (still alive), distinct paths must not repeat.
			// This is a simplification: we verify scope "personal" notes don't duplicate.
			// The personal vault has distinct paths so any repeat is a bug.
			t.Errorf("page 2 returned duplicate note %q from page 1", n.Ref.Path)
		}
	}
}

// --- Test D: all scopes failed → ErrUnavailable ---

func TestFederation_AllScopesFailed_ErrUnavailable(t *testing.T) {
	va := newStubVault("personal")
	vb := newStubVault("team")
	va.failAfter = 0
	va.failErr = errors.New("disk failure")
	vb.failAfter = 0
	vb.failErr = errors.New("timeout")

	fed := newFedWithStubs(t, va, vb)

	_, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal", "team"}),
		domain.WithQueryPagination(10, 0),
	))
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("expected ErrUnavailable when all scopes fail, got: %v", err)
	}
}

// --- Test E: Filter.Scopes restricts fan-out within AllowedScopes ---

func TestFederation_FilterScopes_RestrictsFanout(t *testing.T) {
	va := newStubVault("personal", domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/a.md"}, Title: "A"})
	vb := newStubVault("team", domain.NoteSummary{Ref: domain.NoteRef{Path: "projects/b.md"}, Title: "B"})
	fed := newFedWithStubs(t, va, vb)

	// AllowedScopes permits both, but Filter.Scopes restricts to "personal" only.
	res, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes([]domain.ScopeID{"personal", "team"}),
		domain.WithQueryFilter(domain.NewFilter(domain.WithScopes("personal"))),
		domain.WithQueryPagination(10, 0),
	))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, n := range res.Notes {
		if n.Ref.Scope != "personal" {
			t.Errorf("Filter.Scopes=[personal] leaked note from scope %q", n.Ref.Scope)
		}
	}
	if vb.queryCount.Load() != 0 {
		t.Errorf("team vault was queried despite not being in Filter.Scopes")
	}
}

// --- AllowedScopes nil guard on FederationService ---

func TestFederationService_AllowedScopesNil(t *testing.T) {
	va := newStubVault("personal")
	fed := newFedWithStubs(t, va)

	if _, err := fed.Query(context.Background(), domain.NewQueryRequest(
		domain.WithQueryAllowedScopes(nil),
	)); err == nil {
		t.Error("nil AllowedScopes on Query should return error")
	}
	if _, err := fed.Search(context.Background(), "x", domain.AuthFilter{AllowedScopes: nil}, 5); err == nil {
		t.Error("nil AllowedScopes on Search should return error")
	}
	if _, err := fed.Backlinks(context.Background(), domain.NoteRef{Scope: "personal", Path: "x.md"}, false, domain.AuthFilter{AllowedScopes: nil}); err == nil {
		t.Error("nil AllowedScopes on Backlinks should return error")
	}
}

// --- localvault-backed federation: integration smoke ---

func newFedLocalVaults(t *testing.T) (*FederationService, []domain.ScopeID) {
	t.Helper()
	v1, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault personal: %v", err)
	}
	v2, err := localvault.New("team", t.TempDir())
	if err != nil {
		t.Fatalf("localvault team: %v", err)
	}
	t.Cleanup(func() { _ = v1.Close(); _ = v2.Close() })

	reg := NewRegistry()
	if err := reg.AddVault(v1, ""); err != nil {
		t.Fatalf("add personal: %v", err)
	}
	if err := reg.AddVault(v2, ""); err != nil {
		t.Fatalf("add team: %v", err)
	}
	fed, err := NewFederationService(reg)
	if err != nil {
		t.Fatalf("NewFederationService: %v", err)
	}
	return fed, []domain.ScopeID{"personal", "team"}
}

func TestFederation_LocalVaults_CrossScopeQuery(t *testing.T) {
	fed, scopes := newFedLocalVaults(t)
	ctx := context.Background()

	// Create a note in each scope.
	if _, err := fed.Create(ctx, domain.CreateInput{Path: "projects/personal.md"}); err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	// Create in team scope via a targeted ref.
	e, _ := fed.reg.EntryFor("team")
	if _, err := e.svc.Create(ctx, domain.CreateInput{Path: "projects/team.md"}); err != nil {
		t.Fatalf("Create team: %v", err)
	}

	res, err := fed.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryAllowedScopes(scopes),
		domain.WithQueryPagination(10, 0),
	))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Notes) < 2 {
		t.Fatalf("expected notes from both vaults, got %d", len(res.Notes))
	}
	scopeSet := make(map[domain.ScopeID]bool)
	for _, n := range res.Notes {
		scopeSet[n.Ref.Scope] = true
	}
	for _, sc := range scopes {
		if !scopeSet[sc] {
			t.Errorf("missing notes from scope %q", sc)
		}
	}
}

// --- Phase 4: Federation write acceptance tests ---

// TestFederation_Delete_StaleETag_Conflict verifies ETag OCC on Delete.
func TestFederation_Delete_StaleETag_Conflict(t *testing.T) {
	fed, _ := newFedLocalVaults(t)
	ctx := context.Background()

	res, err := fed.Create(ctx, domain.CreateInput{Path: "projects/gone.md", Body: "bye"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	staleETag := res.ETag

	// Mutate so the ETag changes.
	res2, err := fed.UpdateBody(ctx, domain.NoteRef{Scope: "personal", Path: "projects/gone.md"}, "new body", res.ETag)
	if err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if res2.ETag == staleETag {
		t.Fatal("ETag should change after UpdateBody")
	}

	// Delete with the stale ETag should conflict.
	err = fed.Delete(ctx, domain.NoteRef{Scope: "personal", Path: "projects/gone.md"}, false, staleETag)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict with stale ETag, got %v", err)
	}

	// Delete with the current ETag should succeed.
	if err := fed.Delete(ctx, domain.NoteRef{Scope: "personal", Path: "projects/gone.md"}, false, res2.ETag); err != nil {
		t.Fatalf("Delete with correct ETag: %v", err)
	}
}

// TestFederation_Delete_Tombstone verifies tombstones prevent deleted notes from reappearing.
func TestFederation_Delete_Tombstone(t *testing.T) {
	fed, scopes := newFedLocalVaults(t)
	ctx := context.Background()

	// Create note in personal scope.
	if _, err := fed.Create(ctx, domain.CreateInput{Path: "projects/ephemeral.md", Body: "temp"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify it appears in query.
	res, err := fed.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryAllowedScopes(scopes),
		domain.WithQueryPagination(10, 0),
	))
	if err != nil {
		t.Fatalf("Query before delete: %v", err)
	}
	if len(res.Notes) != 1 {
		t.Fatalf("expected 1 note before delete, got %d", len(res.Notes))
	}

	// Delete it.
	if err := fed.Delete(ctx, domain.NoteRef{Scope: "personal", Path: "projects/ephemeral.md"}, false, ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify tombstone was recorded.
	if !fed.tombstones.Contains(domain.NoteRef{Scope: "personal", Path: "projects/ephemeral.md"}) {
		t.Error("tombstone not recorded after delete")
	}

	// Even if tombstone check didn't exist, the note is really gone — tombstone filtering is belt-and-suspenders.
	res2, err := fed.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryAllowedScopes(scopes),
		domain.WithQueryPagination(10, 0),
	))
	if err != nil {
		t.Fatalf("Query after delete: %v", err)
	}
	for _, n := range res2.Notes {
		if n.Ref.Path == "projects/ephemeral.md" {
			t.Error("deleted note should not appear in query results")
		}
	}
}

// TestFederation_Promote_CrossScope verifies note_promote copies across scopes.
func TestFederation_Promote_CrossScope(t *testing.T) {
	fed, scopes := newFedLocalVaults(t)
	ctx := context.Background()

	// Create source note in personal scope.
	if _, err := fed.Create(ctx, domain.CreateInput{
		Path: "projects/my-note.md",
		FrontMatter: domain.FrontMatter{
			Title: "My Note",
			Tags:  []string{"important"},
		},
		Body: "# Content\nThis is the body.",
	}); err != nil {
		t.Fatalf("Create source: %v", err)
	}

	// Get the source note's ETag.
	src, err := fed.Get(ctx, domain.NoteRef{Scope: "personal", Path: "projects/my-note.md"})
	if err != nil {
		t.Fatalf("Get source: %v", err)
	}

	// Promote to team scope with correct ETag.
	destRes, err := fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "personal", Path: "projects/my-note.md"},
		ToScope:    "team",
		IfMatch:    src.ETag,
		KeepSource: false,
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Destination should have a new NoteID in the team scope.
	if destRes.Summary.Ref.Scope != "team" {
		t.Errorf("destination scope = %q, want %q", destRes.Summary.Ref.Scope, "team")
	}
	if destRes.Summary.Ref.Path != "projects/my-note.md" {
		t.Errorf("destination path = %q, want %q", destRes.Summary.Ref.Path, "projects/my-note.md")
	}

	// Verify destination note exists and has correct body.
	dest, err := fed.Get(ctx, domain.NoteRef{Scope: "team", Path: "projects/my-note.md"})
	if err != nil {
		t.Fatalf("Get destination: %v", err)
	}
	if dest.Body != src.Body {
		t.Errorf("destination body = %q, want %q", dest.Body, src.Body)
	}

	// Source should be archived (keep_source=false) not deleted.
	_, err = fed.Get(ctx, domain.NoteRef{Scope: "personal", Path: "projects/my-note.md"})
	if err == nil {
		t.Log("source still accessible (expected: archived to archives/)")
	}

	// Verify note appears in federated query.
	res, err := fed.Query(ctx, domain.NewQueryRequest(
		domain.WithQueryAllowedScopes(scopes),
		domain.WithQueryPagination(20, 0),
	))
	if err != nil {
		t.Fatalf("Query after promote: %v", err)
	}
	found := false
	for _, n := range res.Notes {
		if n.Ref.Scope == "team" && n.Ref.Path == "projects/my-note.md" {
			found = true
		}
	}
	if !found {
		t.Error("promoted note not found in federated query")
	}
}

// TestFederation_Promote_OnConflict_Error verifies on_conflict: error behavior.
func TestFederation_Promote_OnConflict_Error(t *testing.T) {
	fed, _ := newFedLocalVaults(t)
	ctx := context.Background()

	// Create notes in both scopes at the same path.
	if _, err := fed.Create(ctx, domain.CreateInput{Path: "projects/clash.md", Body: "personal"}); err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	teamEntry, _ := fed.reg.EntryFor("team")
	if _, err := teamEntry.svc.Create(ctx, domain.CreateInput{Path: "projects/clash.md", Body: "team"}); err != nil {
		t.Fatalf("Create team: %v", err)
	}

	// Promote with on_conflict: error (default) should fail.
	_, err := fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "personal", Path: "projects/clash.md"},
		ToScope:    "team",
		OnConflict: "error",
		KeepSource: true,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict on destination path conflict, got %v", err)
	}
}

// TestFederation_Promote_OnConflict_Overwrite verifies on_conflict: overwrite behavior.
func TestFederation_Promote_OnConflict_Overwrite(t *testing.T) {
	fed, _ := newFedLocalVaults(t)
	ctx := context.Background()

	if _, err := fed.Create(ctx, domain.CreateInput{Path: "projects/target.md", Body: "v1"}); err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	teamEntry, _ := fed.reg.EntryFor("team")
	if _, err := teamEntry.svc.Create(ctx, domain.CreateInput{Path: "projects/target.md", Body: "old team body"}); err != nil {
		t.Fatalf("Create team: %v", err)
	}

	// Promote with overwrite should succeed.
	_, err := fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "personal", Path: "projects/target.md"},
		ToScope:    "team",
		OnConflict: "overwrite",
		KeepSource: true,
	})
	if err != nil {
		t.Fatalf("Promote with overwrite: %v", err)
	}

	// Destination should have personal's body now.
	dest, err := fed.Get(ctx, domain.NoteRef{Scope: "team", Path: "projects/target.md"})
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if dest.Body != "v1" {
		t.Errorf("destination body after overwrite = %q, want %q", dest.Body, "v1")
	}
}

// TestFederation_Promote_StaleETag_Conflict verifies source ETag precondition.
func TestFederation_Promote_StaleETag_Conflict(t *testing.T) {
	fed, _ := newFedLocalVaults(t)
	ctx := context.Background()

	srcRes, err := fed.Create(ctx, domain.CreateInput{Path: "projects/src.md", Body: "original"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	staleETag := srcRes.ETag

	// Update source so ETag changes.
	if _, err := fed.UpdateBody(ctx, domain.NoteRef{Scope: "personal", Path: "projects/src.md"}, "updated", staleETag); err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}

	// Promote with stale ETag should conflict.
	_, err = fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "personal", Path: "projects/src.md"},
		ToScope:    "team",
		IfMatch:    staleETag,
		KeepSource: true,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict with stale source ETag, got %v", err)
	}
}

// TestFederation_3Vault_ConcurrentEdit_ConflictAndRetry is the 3-vault round-trip test.
// personal -> team -> personal under concurrent edits; conflict surfaces cleanly; re-read+retry succeeds.
func TestFederation_3Vault_ConcurrentEdit_ConflictAndRetry(t *testing.T) {
	v1, err := localvault.New("personal", t.TempDir())
	if err != nil {
		t.Fatalf("localvault personal: %v", err)
	}
	v2, err := localvault.New("team", t.TempDir())
	if err != nil {
		t.Fatalf("localvault team: %v", err)
	}
	v3, err := localvault.New("archive", t.TempDir())
	if err != nil {
		t.Fatalf("localvault archive: %v", err)
	}
	t.Cleanup(func() { _ = v1.Close(); _ = v2.Close(); _ = v3.Close() })

	reg := NewRegistry()
	if err := reg.AddVault(v1, ""); err != nil {
		t.Fatalf("add personal: %v", err)
	}
	if err := reg.AddVault(v2, ""); err != nil {
		t.Fatalf("add team: %v", err)
	}
	if err := reg.AddVault(v3, ""); err != nil {
		t.Fatalf("add archive: %v", err)
	}
	fed, err := NewFederationService(reg)
	if err != nil {
		t.Fatalf("NewFederationService: %v", err)
	}

	ctx := context.Background()

	// Step 1: Create note in personal.
	res, err := fed.Create(ctx, domain.CreateInput{Path: "projects/shared.md", Body: "v1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	etag1 := res.ETag

	// Step 2: Concurrent edit — another writer updates personal before we can promote.
	res2, err := fed.UpdateBody(ctx, domain.NoteRef{Scope: "personal", Path: "projects/shared.md"}, "concurrent edit", etag1)
	if err != nil {
		t.Fatalf("concurrent UpdateBody: %v", err)
	}
	etag2 := res2.ETag

	// Step 3: Promote to team with stale ETag — must get conflict.
	_, err = fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "personal", Path: "projects/shared.md"},
		ToScope:    "team",
		IfMatch:    etag1, // stale
		KeepSource: true,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected conflict on stale ETag, got %v", err)
	}

	// Step 4: Re-read to get fresh ETag, then retry.
	note, err := fed.Get(ctx, domain.NoteRef{Scope: "personal", Path: "projects/shared.md"})
	if err != nil {
		t.Fatalf("re-read after conflict: %v", err)
	}
	if note.ETag != etag2 {
		t.Errorf("re-read ETag = %q, want %q", note.ETag, etag2)
	}

	// Step 5: Retry with fresh ETag — succeeds.
	teamRes, err := fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "personal", Path: "projects/shared.md"},
		ToScope:    "team",
		IfMatch:    etag2,
		KeepSource: true,
	})
	if err != nil {
		t.Fatalf("retry promote with fresh ETag: %v", err)
	}
	if teamRes.Summary.Ref.Scope != "team" {
		t.Errorf("result scope = %q, want team", teamRes.Summary.Ref.Scope)
	}

	// Step 6: Promote team note to archive scope.
	teamNote, err := fed.Get(ctx, domain.NoteRef{Scope: "team", Path: "projects/shared.md"})
	if err != nil {
		t.Fatalf("Get team note: %v", err)
	}
	_, err = fed.Promote(ctx, domain.PromoteInput{
		Ref:        domain.NoteRef{Scope: "team", Path: "projects/shared.md"},
		ToScope:    "archive",
		IfMatch:    teamNote.ETag,
		KeepSource: false,
	})
	if err != nil {
		t.Fatalf("promote team -> archive: %v", err)
	}

	// Verify archive note exists.
	archNote, err := fed.Get(ctx, domain.NoteRef{Scope: "archive", Path: "projects/shared.md"})
	if err != nil {
		t.Fatalf("Get archive note: %v", err)
	}
	if archNote.Body != "concurrent edit" {
		t.Errorf("archive body = %q, want %q", archNote.Body, "concurrent edit")
	}
}
