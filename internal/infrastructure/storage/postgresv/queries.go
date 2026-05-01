package postgresv

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

func (v *PostgresVault) Get(ctx context.Context, path string) (domain.Note, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.Note{}, err
	}
	var r noteRow
	err = v.pool.QueryRow(ctx,
		`SELECT scope, path, body, etag, created_at, updated_at,
		        title, tags, status, area, project
		 FROM notes
		 WHERE scope = $1 AND path = $2 AND deleted_at IS NULL`,
		v.scope, np.Storage,
	).Scan(
		&r.scope, &r.path, &r.body, &r.etag,
		&r.createdAt, &r.updatedAt,
		&r.title, &r.tags, &r.status, &r.area, &r.project,
	)
	if err != nil {
		if isNoRows(err) {
			return domain.Note{}, domain.ErrNotFound
		}
		return domain.Note{}, err
	}
	return r.toNote(false), nil
}

func (v *PostgresVault) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
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

func (v *PostgresVault) Search(_ context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
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

func (v *PostgresVault) Backlinks(_ context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error) {
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

func (v *PostgresVault) Stats(_ context.Context) (domain.VaultStats, error) {
	stats := domain.VaultStats{ByCategory: make(map[domain.Category]int)}
	v.cache.Iterate(func(_ string, s domain.NoteSummary) {
		stats.TotalNotes++
		stats.ByCategory[s.Category]++
	})
	return stats, nil
}

func (v *PostgresVault) Health(_ context.Context) (domain.VaultHealth, error) {
	return domain.VaultHealth{WatcherStatus: "postgres"}, nil
}

func (v *PostgresVault) Rescan(ctx context.Context) error {
	v.cache = noteutil.NewNoteCache()
	v.graph = noteutil.NewBacklinkGraph()
	v.idx.Close()
	v.idx = newIndex()
	return v.loadAll(ctx)
}
