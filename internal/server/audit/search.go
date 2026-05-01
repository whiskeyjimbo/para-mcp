package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// SearchFilter specifies which audit rows to return.
// Zero values for any field mean "no constraint on that field".
type SearchFilter struct {
	Actor   string
	Scope   string // matches scope_local OR scope_canonical
	Action  string
	Outcome string
	Since   time.Time
	Until   time.Time
	Limit   int // 0 → DefaultSearchLimit
	Offset  int
}

const DefaultSearchLimit = 50

// Searcher can query the audit log. Implemented by backends that support
// search (FileBackend). The stderrBackend intentionally does not.
type Searcher interface {
	Search(ctx context.Context, f SearchFilter) ([]Row, error)
}

// Search scans the log file in reverse-chronological order and returns rows
// matching f. The file is read forward then the matching slice is reversed.
func (fb *FileBackend) Search(_ context.Context, f SearchFilter) ([]Row, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	fb.mu.Lock()
	path := fb.f.Name()
	fb.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matched []Row
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row Row
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if !matches(row, f) {
			continue
		}
		matched = append(matched, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Reverse to get descending timestamp order.
	for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}

	// Apply offset + limit.
	if f.Offset >= len(matched) {
		return []Row{}, nil
	}
	matched = matched[f.Offset:]
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func matches(row Row, f SearchFilter) bool {
	if f.Actor != "" && row.Actor != f.Actor {
		return false
	}
	if f.Action != "" && row.Action != f.Action {
		return false
	}
	if f.Outcome != "" && row.Outcome != f.Outcome {
		return false
	}
	if f.Scope != "" && row.ScopeLocal != f.Scope && row.ScopeCanonical != f.Scope {
		return false
	}
	if !f.Since.IsZero() && row.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && row.Timestamp.After(f.Until) {
		return false
	}
	return true
}
