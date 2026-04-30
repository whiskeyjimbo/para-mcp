package localvault

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ConflictDetector identifies and resolves sync-conflict filenames.
type ConflictDetector struct {
	patterns []*regexp.Regexp
}

func newConflictDetector(patterns []*regexp.Regexp) *ConflictDetector {
	return &ConflictDetector{patterns: patterns}
}

// IsConflict reports whether name matches any conflict pattern.
func (cd *ConflictDetector) IsConflict(name string) bool {
	for _, re := range cd.patterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// ResolveCanonical strips the conflict suffix from conflictPath and returns
// the path of the original file, or "" if no pattern matches.
func (cd *ConflictDetector) ResolveCanonical(conflictPath string) string {
	dir := filepath.Dir(conflictPath)
	base := filepath.Base(conflictPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for _, re := range cd.patterns {
		cleaned := strings.TrimSpace(re.ReplaceAllString(stem, ""))
		if cleaned != stem && cleaned != "" {
			return filepath.Join(dir, cleaned+ext)
		}
	}
	return ""
}
