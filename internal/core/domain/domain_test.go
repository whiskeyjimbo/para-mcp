package domain

import (
	"strings"
	"testing"
)

func TestCategoryFromPath(t *testing.T) {
	cases := []struct {
		path string
		want Category
		ok   bool
	}{
		{"projects/foo.md", Projects, true},
		{"areas/work.md", Areas, true},
		{"resources/aws.md", Resources, true},
		{"archives/old.md", Archives, true},
		{"Projects/Foo.md", Projects, true},
		{"random/foo.md", "", false},
		{"foo.md", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := CategoryFromPath(c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("CategoryFromPath(%q) = (%q, %v), want (%q, %v)", c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestNoteCategory(t *testing.T) {
	n := Note{Ref: NoteRef{Scope: "personal", Path: "projects/foo.md"}}
	if got := n.Category(); got != Projects {
		t.Errorf("got %q, want %q", got, Projects)
	}
}

func TestNoteRefString(t *testing.T) {
	ref := NoteRef{Scope: "personal", Path: "projects/infra-refactor.md"}
	if got := ref.String(); got != "personal:projects/infra-refactor.md" {
		t.Errorf("got %q", got)
	}
}

func TestNormalizeTag(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"aws", "aws", false},
		{"AWS", "aws", false},
		{"#aws", "aws", false},
		{" AWS ", "aws", false},
		{"Aws", "aws", false},
		{"#AWS", "aws", false},
		{"multi word", "multi-word", false},
		{"  spaces  everywhere  ", "spaces-everywhere", false},
		{"hello world", "hello-world", false},
		{"", "", true},
		{"#", "", true},
		{strings.Repeat("a", 65), "", true},
		{strings.Repeat("a", 64), strings.Repeat("a", 64), false},
	}
	for _, c := range cases {
		got, err := NormalizeTag(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeTag(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeTag(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeTag_CollapseRuns(t *testing.T) {
	got, err := NormalizeTag("spaces  everywhere")
	if err != nil {
		t.Fatal(err)
	}
	if got != "spaces-everywhere" {
		t.Errorf("got %q, want %q", got, "spaces-everywhere")
	}
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{"AWS", "#go", "  infra  ", ""})
	want := []string{"aws", "go", "infra"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeTags: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NormalizeTags[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeStatus(t *testing.T) {
	if got := NormalizeStatus("Active"); got != "active" {
		t.Errorf("got %q, want %q", got, "active")
	}
	if got := NormalizeStatus(""); got != "" {
		t.Errorf("empty status should pass through unchanged, got %q", got)
	}
}

func TestApplyFrontMatterPatch(t *testing.T) {
	fm := FrontMatter{Title: "old", Status: "active", Tags: []string{"go"}}
	ApplyFrontMatterPatch(&fm, map[string]any{
		"title":  "new",
		"status": "DONE",
		"tags":   []string{"AWS", "infra"},
		"custom": "value",
	})
	if fm.Title != "new" {
		t.Errorf("title: got %q", fm.Title)
	}
	if fm.Status != "done" {
		t.Errorf("status: got %q", fm.Status)
	}
	if len(fm.Tags) != 2 || fm.Tags[0] != "aws" || fm.Tags[1] != "infra" {
		t.Errorf("tags: got %v", fm.Tags)
	}
	if fm.Extra["custom"] != "value" {
		t.Errorf("extra: got %v", fm.Extra)
	}
}

func TestScoreRelatedness(t *testing.T) {
	target := Note{FrontMatter: FrontMatter{Tags: []string{"go", "aws"}, Area: "platform", Project: "infra"}}
	cases := []struct {
		candidate NoteSummary
		want      float64
	}{
		{NoteSummary{Tags: []string{"go", "aws"}, Area: "platform", Project: "infra"}, 6},
		{NoteSummary{Tags: []string{"go"}, Area: "platform"}, 3},
		{NoteSummary{Tags: []string{"python"}}, 0},
		{NoteSummary{}, 0},
	}
	for _, c := range cases {
		if got := ScoreRelatedness(target, c.candidate); got != c.want {
			t.Errorf("ScoreRelatedness: got %v, want %v (candidate %+v)", got, c.want, c.candidate)
		}
	}
}

func TestNormalizeScopeID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"personal", "personal", false},
		{"team-platform", "team-platform", false},
		{"team_data", "team_data", false},
		{"TEAM-Platform", "team-platform", false},
		{" team-platform ", "team-platform", false},
		{"", "", true},
		{"team platform", "", true},
		{"team@platform", "", true},
		{strings.Repeat("a", 65), "", true},
		{strings.Repeat("a", 64), strings.Repeat("a", 64), false},
	}
	for _, c := range cases {
		got, err := NormalizeScopeID(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeScopeID(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeScopeID(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeScopeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
