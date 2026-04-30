package localvault

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

func (v *LocalVault) Get(_ context.Context, path string) (domain.Note, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.Note{}, err
	}
	return v.readNote(np.Storage)
}

func (v *LocalVault) Query(_ context.Context, q domain.QueryRequest) (domain.QueryResult, error) {
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

func (v *LocalVault) Search(_ context.Context, text string, filter domain.Filter, limit int) ([]domain.RankedNote, error) {
	if limit <= 0 {
		limit = 20
	}
	hits := v.idx.Search(text, limit*3)
	var results []domain.RankedNote
	for _, h := range hits {
		key := indexKey(h.Ref.Path, v.caps.CaseSensitive)
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

func (v *LocalVault) Backlinks(_ context.Context, ref domain.NoteRef, includeAssets bool, filter domain.Filter) ([]domain.BacklinkEntry, error) {
	keys := linkMatchKeys(ref.Path)
	seen := make(map[string]bool)
	var entries []domain.BacklinkEntry
	for _, key := range keys {
		for _, src := range v.graph.Backlinks(key) {
			if !includeAssets && src.isAsset {
				continue
			}
			if seen[src.path] {
				continue
			}
			seen[src.path] = true
			srcKey := indexKey(src.path, v.caps.CaseSensitive)
			s, ok := v.cache.Get(srcKey)
			if !ok {
				continue
			}
			if !domain.MatchesFilter(s, filter) {
				continue
			}
			entries = append(entries, domain.BacklinkEntry{Summary: s, IsAsset: src.isAsset})
		}
	}
	return entries, nil
}

func (v *LocalVault) Stats(_ context.Context) (domain.VaultStats, error) {
	stats := domain.VaultStats{ByCategory: make(map[domain.Category]int)}
	v.cache.Iterate(func(_ string, s domain.NoteSummary) {
		stats.TotalNotes++
		stats.ByCategory[s.Category]++
	})
	return stats, nil
}

func (v *LocalVault) Health(_ context.Context) (domain.VaultHealth, error) {
	h := domain.VaultHealth{
		WatcherStatus:     v.w.watcherStatus.Load().(string),
		SyncConflicts:     int(v.w.syncConflicts.Load()),
		UnrecognizedFiles: v.countUnrecognized(),
	}
	if v.caps.CaseSensitive {
		h.CaseCollisions = v.detectCaseCollisions()
	}
	return h, nil
}

func (v *LocalVault) Rescan(_ context.Context) error {
	return v.scanVault()
}

func (v *LocalVault) countUnrecognized() int {
	var count int
	_ = filepath.WalkDir(v.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(v.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		first, _, _ := strings.Cut(rel, "/")
		if strings.HasPrefix(first, ".") {
			return filepath.SkipDir
		}
		if isMDFile(path) {
			if _, ok := domain.CategoryFromPath(rel); ok {
				return nil
			}
		}
		count++
		return nil
	})
	return count
}

func (v *LocalVault) detectCaseCollisions() []domain.CaseCollision {
	lower := make(map[string]string, v.cache.Len())
	var collisions []domain.CaseCollision
	v.cache.Iterate(func(key string, s domain.NoteSummary) {
		lk := indexKey(key, false)
		if prev, exists := lower[lk]; exists && prev != key {
			collisions = append(collisions, domain.CaseCollision{PathA: prev, PathB: s.Ref.Path})
		} else {
			lower[lk] = key
		}
	})
	return collisions
}
