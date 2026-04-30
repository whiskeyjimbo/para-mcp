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
func Normalize(path string, caseSensitive bool) (NormalizedPath, error) {
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
