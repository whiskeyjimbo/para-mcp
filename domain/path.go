package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// NormalizedPath is the result of Normalize: two forms of the same path.
type NormalizedPath struct {
	// Storage is NFC-normalized with original case. Use for filesystem I/O.
	Storage string
	// IndexKey is NFC-normalized and lowercased on case-insensitive vaults.
	// Use for index lookups and BM25 keys.
	IndexKey string
}

// Normalize validates a vault-relative path and returns its canonical forms.
//
// Validation rules (in order):
//  1. Reject absolute paths.
//  2. Reject paths containing ".." segments after path.Clean.
//  3. Best-effort: reject paths that resolve via symlinks outside vaultRoot
//     (skipped when the target does not yet exist — e.g. create operations).
//  4. Reject paths whose first segment is not a known PARA category
//     (projects|areas|resources|archives) or ".trash".
//  5. Reject paths containing null bytes or backslashes.
//  6. Apply Unicode NFC normalization.
//  7. Produce IndexKey as lowercase on case-insensitive vaults.
//
// vaultRoot may be empty to skip rule 3 (useful in unit tests and domain logic).
func Normalize(vaultRoot, path string, caseSensitive bool) (NormalizedPath, error) {
	// Rule 5: null bytes and backslashes before anything else.
	if strings.ContainsRune(path, 0) {
		return NormalizedPath{}, errors.New("path contains null byte")
	}
	if strings.ContainsRune(path, '\\') {
		return NormalizedPath{}, errors.New("path contains backslash")
	}

	// Rule 1: reject absolute paths.
	if filepath.IsAbs(path) {
		return NormalizedPath{}, errors.New("path must be relative")
	}

	// Rule 6: NFC normalization before any structural analysis so that
	// decomposed (NFD) paths from macOS HFS+/APFS map to the same key.
	path = norm.NFC.String(path)

	// Rule 2: reject ".." traversal after clean.
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return NormalizedPath{}, errors.New("path traverses above vault root")
	}

	// Rule 4: first path segment must be a known PARA category or .trash.
	seg, _, _ := strings.Cut(cleaned, "/")
	switch strings.ToLower(seg) {
	case "projects", "areas", "resources", "archives", ".trash":
	default:
		return NormalizedPath{}, fmt.Errorf("path must begin with a PARA category (projects|areas|resources|archives|.trash), got %q", seg)
	}

	// Rule 3: symlink check — best-effort, skipped when target doesn't exist.
	if vaultRoot != "" {
		if err := checkSymlinks(vaultRoot, cleaned); err != nil {
			return NormalizedPath{}, err
		}
	}

	indexKey := cleaned
	if !caseSensitive {
		indexKey = strings.ToLower(cleaned)
	}

	return NormalizedPath{Storage: cleaned, IndexKey: indexKey}, nil
}

// checkSymlinks verifies that path (relative to vaultRoot) does not resolve
// outside vaultRoot via symlinks. Skips the check when the path doesn't exist.
func checkSymlinks(vaultRoot, path string) error {
	abs := filepath.Join(vaultRoot, path)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// File doesn't exist yet (create operation) or symlinks unresolvable —
		// fall back to checking the nearest existing parent.
		real, err = evalSymlinksPartial(abs)
		if err != nil {
			return nil // can't check; accept
		}
	}
	vaultReal, err := filepath.EvalSymlinks(vaultRoot)
	if err != nil {
		return nil // can't check vault root itself; accept
	}
	prefix := vaultReal + string(filepath.Separator)
	if real != vaultReal && !strings.HasPrefix(real, prefix) {
		return fmt.Errorf("path resolves outside vault root")
	}
	return nil
}

// IndexKey returns the index lookup key for a normalized path.
// On case-insensitive vaults (caseSensitive=false) the key is lowercased.
func IndexKey(path string, caseSensitive bool) string {
	if caseSensitive {
		return path
	}
	return strings.ToLower(path)
}

// evalSymlinksPartial walks up the path until it finds an existing component
// and evaluates symlinks on that, returning the real path of the deepest
// existing ancestor.
func evalSymlinksPartial(abs string) (string, error) {
	p := abs
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return "", errors.New("no existing ancestor found")
		}
		p = parent
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return real, nil
		}
	}
}
