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
		{"Projects/Foo.md", Projects, true}, // case-insensitive first segment
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
	// "spaces  everywhere" has two spaces between words -> one '-' (run collapsed)
	got, err := NormalizeTag("spaces  everywhere")
	if err != nil {
		t.Fatal(err)
	}
	if got != "spaces-everywhere" {
		t.Errorf("got %q, want %q", got, "spaces-everywhere")
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
		{"team platform", "", true}, // space not allowed
		{"team@platform", "", true}, // @ not allowed
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
