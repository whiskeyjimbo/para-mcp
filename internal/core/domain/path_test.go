package domain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalize_ValidPaths(t *testing.T) {
	cases := []string{
		"projects/foo.md",
		"areas/work/notes.md",
		"resources/aws.md",
		"archives/2024/old.md",
		".trash/deleted.md",
	}
	for _, p := range cases {
		got, err := Normalize("", p, true)
		if err != nil {
			t.Errorf("Normalize(%q): unexpected error: %v", p, err)
			continue
		}
		if got.Storage == "" {
			t.Errorf("Normalize(%q): empty Storage", p)
		}
	}
}

func TestNormalize_RejectsAbsolute(t *testing.T) {
	_, err := Normalize("", "/etc/passwd", true)
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestNormalize_RejectsDotDot(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"projects/../../etc/passwd",
		"projects/../../../secret",
	}
	for _, p := range cases {
		_, err := Normalize("", p, true)
		if err == nil {
			t.Errorf("Normalize(%q): expected error for .. traversal", p)
		}
	}
}

func TestNormalize_RejectsNullByte(t *testing.T) {
	_, err := Normalize("", "projects/foo\x00bar.md", true)
	if err == nil {
		t.Fatal("expected error for null byte")
	}
}

func TestNormalize_RejectsBackslash(t *testing.T) {
	_, err := Normalize("", `projects\foo.md`, true)
	if err == nil {
		t.Fatal("expected error for backslash")
	}
}

func TestNormalize_RejectsNonPARARoot(t *testing.T) {
	cases := []string{
		"random/foo.md",
		"foo.md",
		"notes/bar.md",
	}
	for _, p := range cases {
		_, err := Normalize("", p, true)
		if err == nil {
			t.Errorf("Normalize(%q): expected error for non-PARA root", p)
		}
	}
}

func TestNormalize_NFCNormalization(t *testing.T) {
	nfd := "projects/café.md"
	nfc := "projects/café.md"

	gotNFD, err := Normalize("", nfd, true)
	if err != nil {
		t.Fatalf("Normalize(NFD): %v", err)
	}
	gotNFC, err := Normalize("", nfc, true)
	if err != nil {
		t.Fatalf("Normalize(NFC): %v", err)
	}
	if gotNFD.Storage != gotNFC.Storage {
		t.Errorf("NFD and NFC paths should produce same Storage: %q vs %q", gotNFD.Storage, gotNFC.Storage)
	}
}

func TestNormalize_IndexKeyCaseSensitive(t *testing.T) {
	got, err := Normalize("", "projects/Foo.md", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.IndexKey != got.Storage {
		t.Errorf("case-sensitive: IndexKey should equal Storage, got %q vs %q", got.IndexKey, got.Storage)
	}
}

func TestNormalize_IndexKeyCaseInsensitive(t *testing.T) {
	got, err := Normalize("", "projects/Foo.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Storage != "projects/Foo.md" {
		t.Errorf("Storage should preserve case: %q", got.Storage)
	}
	if got.IndexKey != "projects/foo.md" {
		t.Errorf("IndexKey should be lowercase: %q", got.IndexKey)
	}
}

func TestNormalize_CategoryCaseInsensitiveFirstSeg(t *testing.T) {
	got, err := Normalize("", "Projects/Foo.md", true)
	if err != nil {
		t.Fatalf("Normalize(%q): unexpected error: %v", "Projects/Foo.md", err)
	}
	if got.Storage == "" {
		t.Error("expected non-empty Storage")
	}
}

func TestNormalize_SymlinkOutsideVault(t *testing.T) {
	vault := t.TempDir()
	outsideTarget := t.TempDir()

	projDir := filepath.Join(vault, "projects")
	if err := os.Mkdir(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evilFile := filepath.Join(outsideTarget, "evil.md")
	if err := os.WriteFile(evilFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(projDir, "evil.md")
	if err := os.Symlink(evilFile, linkPath); err != nil {
		t.Fatal(err)
	}

	_, err := Normalize(vault, "projects/evil.md", true)
	if err == nil {
		t.Fatal("expected error for symlink pointing outside vault")
	}
}

func TestNormalize_SymlinkInsideVault(t *testing.T) {
	vault := t.TempDir()
	projDir := filepath.Join(vault, "projects")
	if err := os.Mkdir(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(projDir, "real.md")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(projDir, "alias.md")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}

	_, err := Normalize(vault, "projects/alias.md", true)
	if err != nil {
		t.Fatalf("expected no error for in-vault symlink: %v", err)
	}
}

func TestNormalize_NonExistentPathSkipsSymlinkCheck(t *testing.T) {
	vault := t.TempDir()
	_, err := Normalize(vault, "projects/new-note.md", true)
	if err != nil {
		t.Fatalf("non-existent path should not error: %v", err)
	}
}
