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
	IndexKey string
}

// Normalize validates a vault-relative path and returns its canonical forms.
func Normalize(vaultRoot, path string, caseSensitive bool) (NormalizedPath, error) {
	if strings.ContainsRune(path, 0) {
		return NormalizedPath{}, errors.New("path contains null byte")
	}
	if strings.ContainsRune(path, '\\') {
		return NormalizedPath{}, errors.New("path contains backslash")
	}
	if filepath.IsAbs(path) {
		return NormalizedPath{}, errors.New("path must be relative")
	}

	path = norm.NFC.String(path)

	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return NormalizedPath{}, errors.New("path traverses above vault root")
	}

	seg, _, _ := strings.Cut(cleaned, "/")
	switch strings.ToLower(seg) {
	case "projects", "areas", "resources", "archives", ".trash":
	default:
		return NormalizedPath{}, fmt.Errorf("path must begin with a PARA category (projects|areas|resources|archives|.trash), got %q", seg)
	}

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

// IndexKey returns the index lookup key for a normalized path.
func IndexKey(path string, caseSensitive bool) string {
	if caseSensitive {
		return path
	}
	return strings.ToLower(path)
}

// ArchivePath returns the archives/ equivalent of path by replacing its
// first path segment with "archives".
func ArchivePath(path string) (string, error) {
	_, rest, ok := strings.Cut(path, "/")
	if !ok {
		return "", fmt.Errorf("path has no directory segment: %q", path)
	}
	return "archives/" + rest, nil
}

func checkSymlinks(vaultRoot, path string) error {
	abs := filepath.Join(vaultRoot, path)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real, err = evalSymlinksPartial(abs)
		if err != nil {
			return nil
		}
	}
	vaultReal, err := filepath.EvalSymlinks(vaultRoot)
	if err != nil {
		return nil
	}
	prefix := vaultReal + string(filepath.Separator)
	if real != vaultReal && !strings.HasPrefix(real, prefix) {
		return fmt.Errorf("path resolves outside vault root")
	}
	return nil
}

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
