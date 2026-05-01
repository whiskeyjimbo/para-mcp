package tombstone

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

// entry is one persisted tombstone record.
type entry struct {
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	NoteID string `json:"note_id,omitempty"`
}

// FileStore persists tombstones to a JSON file so they survive gateway restarts.
type FileStore struct {
	mu      sync.RWMutex
	path    string
	entries map[string]string // key: scope:path → noteID
}

// New loads the tombstone file at path (creating it if absent) and returns a FileStore.
func New(path string) (*FileStore, error) {
	s := &FileStore{path: path, entries: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var records []entry
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	for _, r := range records {
		ref := domain.NoteRef{Scope: r.Scope, Path: r.Path}
		s.entries[ref.String()] = r.NoteID
	}
	return s, nil
}

func (s *FileStore) Add(ref domain.NoteRef, noteID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[ref.String()] = noteID
	_ = s.flush()
}

func (s *FileStore) Contains(ref domain.NoteRef) bool {
	s.mu.RLock()
	_, ok := s.entries[ref.String()]
	s.mu.RUnlock()
	return ok
}

func (s *FileStore) flush() error {
	records := make([]entry, 0, len(s.entries))
	for k, noteID := range s.entries {
		// k is "scope:path" — split on first colon.
		scope, path, _ := splitRef(k)
		records = append(records, entry{Scope: scope, Path: path, NoteID: noteID})
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func splitRef(s string) (scope, path, rest string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], ""
		}
	}
	return s, "", ""
}
