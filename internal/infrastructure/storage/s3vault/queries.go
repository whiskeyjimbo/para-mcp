package s3vault

import (
	"context"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/index"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

func (v *S3Vault) Get(ctx context.Context, path string) (domain.Note, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.Note{}, err
	}
	return v.getObject(ctx, np.Storage)
}

func (v *S3Vault) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
	all := v.cache.All()
	filtered := domain.ApplyFilter(all, q.Filter)
	domain.SortSummaries(filtered, q.Sort, q.Desc)

	total := len(filtered)
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := max(q.Offset, 0)
	if offset >= total {
		return domain.QueryResult{
			Notes:           []domain.NoteSummary{},
			Total:           total,
			ScopesAttempted: []domain.ScopeID{v.scope},
			ScopesSucceeded: []domain.ScopeID{v.scope},
			PerScope:        map[domain.ScopeID]int{v.scope: 0},
		}, nil
	}
	end := min(offset+limit, total)
	page := filtered[offset:end]
	return domain.QueryResult{
		Notes:           page,
		Total:           total,
		HasMore:         end < total,
		PerScope:        map[domain.ScopeID]int{v.scope: len(page)},
		ScopesAttempted: []domain.ScopeID{v.scope},
		ScopesSucceeded: []domain.ScopeID{v.scope},
	}, nil
}

func (v *S3Vault) Search(_ context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	if limit <= 0 {
		limit = 20
	}
	hits := v.idx.Search(text, limit*3)
	var results []domain.RankedNote
	for _, h := range hits {
		key := noteutil.IndexKey(h.Ref.Path, false)
		s, ok := v.cache.Get(key)
		if !ok {
			continue
		}
		if !domain.MatchesFilter(s, filter) {
			continue
		}
		results = append(results, domain.RankedNote{Summary: s, Score: h.Score})
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

func (v *S3Vault) Backlinks(_ context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error) {
	keys := noteutil.LinkMatchKeys(ref.Path)
	seen := make(map[string]bool)
	var entries []domain.BacklinkEntry
	for _, key := range keys {
		for _, src := range v.graph.Backlinks(key) {
			if !includeAssets && src.IsAsset {
				continue
			}
			if seen[src.Path] {
				continue
			}
			seen[src.Path] = true
			srcKey := noteutil.IndexKey(src.Path, false)
			s, ok := v.cache.Get(srcKey)
			if !ok {
				continue
			}
			if !domain.MatchesFilter(s, filter) {
				continue
			}
			entries = append(entries, domain.BacklinkEntry{Summary: s, IsAsset: src.IsAsset})
		}
	}
	return entries, nil
}

func (v *S3Vault) Stats(_ context.Context) (domain.VaultStats, error) {
	stats := domain.VaultStats{ByCategory: make(map[domain.Category]int)}
	v.cache.Iterate(func(_ string, s domain.NoteSummary) {
		stats.TotalNotes++
		stats.ByCategory[s.Category]++
	})
	return stats, nil
}

func (v *S3Vault) Health(_ context.Context) (domain.VaultHealth, error) {
	return domain.VaultHealth{WatcherStatus: "s3"}, nil
}

func (v *S3Vault) Rescan(ctx context.Context) error {
	v.cache = noteutil.NewNoteCache()
	v.graph = noteutil.NewBacklinkGraph()
	v.idx.Close()
	v.idx = index.New()
	return v.loadAll(ctx)
}
