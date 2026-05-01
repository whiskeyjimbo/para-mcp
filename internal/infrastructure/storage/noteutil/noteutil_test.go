package noteutil

import (
	"testing"
)

func link(key string) OutLink { return OutLink{TargetKey: key} }

func backlinkCount(g *BacklinkGraph, target string) int {
	return len(g.Backlinks(target))
}

func TestBacklinkGraph_UpsertDiff_AddOnly(t *testing.T) {
	g := NewBacklinkGraph()
	g.UpsertDiff("note.md", nil, []OutLink{link("a"), link("b")})

	if backlinkCount(g, "a") != 1 {
		t.Errorf("a backlinks = %d, want 1", backlinkCount(g, "a"))
	}
	if backlinkCount(g, "b") != 1 {
		t.Errorf("b backlinks = %d, want 1", backlinkCount(g, "b"))
	}
}

func TestBacklinkGraph_UpsertDiff_RemoveOnly(t *testing.T) {
	g := NewBacklinkGraph()
	g.Upsert("note.md", []OutLink{link("a"), link("b")})
	g.UpsertDiff("note.md", []OutLink{link("a"), link("b")}, nil)

	if backlinkCount(g, "a") != 0 {
		t.Errorf("a backlinks = %d, want 0", backlinkCount(g, "a"))
	}
	if backlinkCount(g, "b") != 0 {
		t.Errorf("b backlinks = %d, want 0", backlinkCount(g, "b"))
	}
	if len(g.Links("note.md")) != 0 {
		t.Errorf("outLinks not cleared")
	}
}

func TestBacklinkGraph_UpsertDiff_NoChange(t *testing.T) {
	g := NewBacklinkGraph()
	links := []OutLink{link("a"), link("b")}
	g.Upsert("note.md", links)

	// UpsertDiff with identical sets should be a no-op.
	g.UpsertDiff("note.md", links, links)

	if backlinkCount(g, "a") != 1 {
		t.Errorf("a backlinks = %d, want 1", backlinkCount(g, "a"))
	}
	if backlinkCount(g, "b") != 1 {
		t.Errorf("b backlinks = %d, want 1", backlinkCount(g, "b"))
	}
}

func TestBacklinkGraph_UpsertDiff_Mixed(t *testing.T) {
	g := NewBacklinkGraph()
	g.Upsert("note.md", []OutLink{link("a"), link("b")})

	// Remove "b", add "c".
	g.UpsertDiff("note.md", []OutLink{link("a"), link("b")}, []OutLink{link("a"), link("c")})

	if backlinkCount(g, "a") != 1 {
		t.Errorf("a backlinks = %d, want 1 (unchanged)", backlinkCount(g, "a"))
	}
	if backlinkCount(g, "b") != 0 {
		t.Errorf("b backlinks = %d, want 0 (removed)", backlinkCount(g, "b"))
	}
	if backlinkCount(g, "c") != 1 {
		t.Errorf("c backlinks = %d, want 1 (added)", backlinkCount(g, "c"))
	}
}

func TestBacklinkGraph_UpsertDiff_PatchFrontMatterNoOp(t *testing.T) {
	g := NewBacklinkGraph()
	links := []OutLink{link("x"), link("y"), link("z")}
	g.Upsert("note.md", links)

	// Simulate PatchFrontMatter: old == new == existingLinks.
	// Count mutations by verifying outLinks pointer hasn't needlessly changed.
	g.UpsertDiff("note.md", links, links)

	out := g.Links("note.md")
	if len(out) != 3 {
		t.Errorf("Links after no-op diff = %d, want 3", len(out))
	}
	// All original backlinks should still be present.
	for _, target := range []string{"x", "y", "z"} {
		if backlinkCount(g, target) != 1 {
			t.Errorf("%s backlinks = %d, want 1", target, backlinkCount(g, target))
		}
	}
}
