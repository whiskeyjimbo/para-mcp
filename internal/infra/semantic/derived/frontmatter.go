package derived

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

var _ Store = (*FrontmatterStore)(nil)

// FrontmatterStore reads and writes DerivedMetadata as a JSON value in the
// derived: YAML key of the markdown file. An in-process mutex + OS flock guards
// concurrent writes.
type FrontmatterStore struct {
	root  string
	mu    sync.Mutex
	index map[string]string // noteID → vault-relative path
}

// NewFrontmatterStore creates a FrontmatterStore rooted at vaultDir.
func NewFrontmatterStore(vaultDir string) *FrontmatterStore {
	return &FrontmatterStore{root: vaultDir, index: map[string]string{}}
}

// Get reads DerivedMetadata from the derived: block in the markdown file.
// Returns domain.ErrNotFound if the block is absent.
func (s *FrontmatterStore) Get(_ context.Context, noteID string) (*domain.DerivedMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getUnlocked(noteID)
}

func (s *FrontmatterStore) getUnlocked(noteID string) (*domain.DerivedMetadata, error) {
	rel, ok := s.index[noteID]
	if !ok {
		return nil, fmt.Errorf("noteID %q not indexed: %w", noteID, domain.ErrNotFound)
	}
	content, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	return parseDerivedBlock(string(content))
}

// Set writes DerivedMetadata into the derived: YAML key, holding flock for the write.
func (s *FrontmatterStore) Set(_ context.Context, noteID string, ref domain.NoteRef, meta *domain.DerivedMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	absPath := filepath.Join(s.root, filepath.FromSlash(ref.Path))

	// Use a stable lockfile so that the flock remains valid after the atomic rename
	// replaces the original inode. The lockfile is never renamed or deleted.
	lockPath := absPath + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lockfile: %w", err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck

	// Read via a dedicated fd opened after acquiring the lock.
	f, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("open note: %w", err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return err
	}
	updated, err := injectDerived(string(raw), meta)
	if err != nil {
		return err
	}

	// Atomic write via temp file + rename.
	tmp, err := os.CreateTemp(filepath.Dir(absPath), ".derived_tmp_*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.WriteString(updated); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return werr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		return cerr
	}
	if rerr := os.Rename(tmpName, absPath); rerr != nil {
		os.Remove(tmpName)
		return rerr
	}

	s.index[noteID] = ref.Path
	return nil
}

// IsEditedByUser reports whether the stored DerivedMetadata has EditedByUser=true.
func (s *FrontmatterStore) IsEditedByUser(_ context.Context, noteID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.getUnlocked(noteID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return meta.EditedByUser, nil
}

// parseDerivedBlock extracts DerivedMetadata from the derived: YAML key.
// The value is expected to be a JSON-encoded string.
func parseDerivedBlock(content string) (*domain.DerivedMetadata, error) {
	const prefix = "derived: "
	for line := range strings.SplitSeq(content, "\n") {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			raw := after
			var meta domain.DerivedMetadata
			if err := json.Unmarshal([]byte(raw), &meta); err != nil {
				return nil, fmt.Errorf("parse derived block: %w", err)
			}
			return &meta, nil
		}
	}
	return nil, domain.ErrNotFound
}

// injectDerived writes the derived: JSON block into markdown front matter.
func injectDerived(content string, meta *domain.DerivedMetadata) (string, error) {
	data, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	line := "derived: " + string(data)

	if !strings.HasPrefix(content, "---") {
		return "---\n" + line + "\n---\n\n" + content, nil
	}
	rest := content[3:]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return "", errors.New("malformed front matter: no closing ---")
	}
	fmBody := before
	afterFM := after // skip "\n---"

	lines := strings.Split(fmBody, "\n")
	filtered := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		if !strings.HasPrefix(l, "derived:") {
			filtered = append(filtered, l)
		}
	}
	filtered = append(filtered, line)
	return "---" + strings.Join(filtered, "\n") + "\n---" + afterFM, nil
}
