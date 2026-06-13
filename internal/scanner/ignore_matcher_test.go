package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewIgnoreMatcher(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".docgraphignore"), "*.pdf\ndrafts/\n")
	mustWrite(t, filepath.Join(dir, ".gitignore"), "secret.md\n")

	m, err := NewIgnoreMatcher(dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"fixture.pdf": true,  // .docgraphignore glob
		"drafts/x.md": true,  // .docgraphignore dir pattern
		"secret.md":   true,  // .gitignore honored
		"notes.md":    false, // not excluded
	}
	for rel, want := range cases {
		if got := m(rel); got != want {
			t.Errorf("matcher(%q) = %v, want %v", rel, got, want)
		}
	}

	// --no-gitignore: .gitignore no longer applies; .docgraphignore still does.
	m2, err := NewIgnoreMatcher(dir, ScanOptions{NoGitignore: true})
	if err != nil {
		t.Fatal(err)
	}
	if m2("secret.md") {
		t.Error("secret.md should NOT match under --no-gitignore")
	}
	if !m2("fixture.pdf") {
		t.Error("fixture.pdf should still match .docgraphignore under --no-gitignore")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
