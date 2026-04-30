package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// NormalizeTags applies NormalizeTag to each element, dropping invalid ones.
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if n, err := NormalizeTag(t); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// NormalizeStatus applies tag normalization rules to a status string.
// Returns s unchanged if it fails normalization.
func NormalizeStatus(s string) string {
	if n, err := NormalizeTag(s); err == nil {
		return n
	}
	return s
}

// NormalizeTag applies canonical tag normalization at every write boundary.
func NormalizeTag(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	s = strings.ToLower(s)
	result := strings.Join(strings.FieldsFunc(s, unicode.IsSpace), "-")
	if result == "" {
		return "", fmt.Errorf("tag normalizes to empty string")
	}
	if utf8.RuneCountInString(result) > 64 {
		return "", fmt.Errorf("tag exceeds 64 runes after normalization")
	}
	return result, nil
}

// NormalizeScopeID applies canonical scope ID normalization.
func NormalizeScopeID(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	if s == "" {
		return "", fmt.Errorf("scope ID is empty")
	}
	if utf8.RuneCountInString(s) > 64 {
		return "", fmt.Errorf("scope ID exceeds 64 runes")
	}
	for _, r := range s {
		if !isSlugRune(r) {
			return "", fmt.Errorf("scope ID contains invalid character %q (only [a-z0-9_-] allowed)", r)
		}
	}
	return s, nil
}

func isSlugRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}
