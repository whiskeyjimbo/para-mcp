package domain

import (
	"cmp"
	"slices"
	"strings"
)

// RankedNote pairs a summary with a relevance score from search.
type RankedNote struct {
	Summary NoteSummary
	Score   float64
}

// ScoreRelatedness scores candidate against target: 1pt per shared tag,
// 2pt for same area, 2pt for same project.
func ScoreRelatedness(target Note, candidate NoteSummary) float64 {
	var score float64
	for _, t := range target.FrontMatter.Tags {
		for _, ct := range candidate.Tags {
			if strings.EqualFold(t, ct) {
				score++
			}
		}
	}
	if target.FrontMatter.Area != "" && strings.EqualFold(target.FrontMatter.Area, candidate.Area) {
		score += 2
	}
	if target.FrontMatter.Project != "" && strings.EqualFold(target.FrontMatter.Project, candidate.Project) {
		score += 2
	}
	return score
}

// RankRelated scores, sorts, and trims candidates relative to target.
// Notes with zero score or whose path matches excludePath are omitted.
// If limit <= 0 all non-zero-scored notes are returned.
func RankRelated(target Note, candidates []NoteSummary, excludePath string, limit int) []RankedNote {
	var ranked []RankedNote
	for _, c := range candidates {
		if c.Ref.Path == excludePath {
			continue
		}
		if score := ScoreRelatedness(target, c); score > 0 {
			ranked = append(ranked, RankedNote{Summary: c, Score: score})
		}
	}
	slices.SortFunc(ranked, func(a, b RankedNote) int {
		return cmp.Compare(b.Score, a.Score)
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}
