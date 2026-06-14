package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func fixtureDir(t *testing.T, project string) string {
	t.Helper()
	// Tests run from the package directory, so go up to project root
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", project))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFile creates parent directories as needed and writes data to path.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanOrFatal calls ScanDir and fatals on error.
func scanOrFatal(t *testing.T, dir string) []FileEntry {
	t.Helper()
	entries, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	return entries
}

// relPathSet returns the set of RelPath values from entries.
func relPathSet(entries []FileEntry) map[string]bool {
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[e.RelPath] = true
	}
	return set
}

// relPathList returns a sorted slice of RelPath values for use in error messages.
func relPathList(entries []FileEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.RelPath
	}
	return names
}

func TestScanDirBasic(t *testing.T) {
	t.Run("project-a has 4 markdown files", func(t *testing.T) {
		entries, err := ScanDir(fixtureDir(t, "project-a"))
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) != 4 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.RelPath
			}
			t.Fatalf("expected 4 files, got %d: %v", len(entries), names)
		}
	})
}

func TestScanDirGitignore(t *testing.T) {
	t.Run("project-c excludes secret.md via .gitignore", func(t *testing.T) {
		entries, err := ScanDir(fixtureDir(t, "project-c"))
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) != 1 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.RelPath
			}
			t.Fatalf("expected 1 file, got %d: %v", len(entries), names)
		}
		if entries[0].RelPath != "chinese.md" {
			t.Fatalf("expected chinese.md, got %s", entries[0].RelPath)
		}
	})
}

func TestScanDirSkipDirs(t *testing.T) {
	t.Run("node_modules is skipped", func(t *testing.T) {
		tmp := t.TempDir()

		writeFile(t, filepath.Join(tmp, "node_modules", "test.md"), []byte("# hidden"))
		// Also place a visible file at the root
		writeFile(t, filepath.Join(tmp, "visible.md"), []byte("# visible"))

		entries := scanOrFatal(t, tmp)
		if len(entries) != 1 {
			t.Fatalf("expected 1 file (visible.md only), got %d: %v", len(entries), relPathList(entries))
		}
		if entries[0].RelPath != "visible.md" {
			t.Fatalf("expected visible.md, got %s", entries[0].RelPath)
		}
	})

	t.Run(".claude/worktrees is skipped but .claude/skills is indexed", func(t *testing.T) {
		tmp := t.TempDir()

		writeFile(t, filepath.Join(tmp, ".claude", "worktrees", "agent-x", "dup.md"), []byte("# duplicate repo copy"))
		writeFile(t, filepath.Join(tmp, ".claude", "skills", "foo", "SKILL.md"), []byte("# skill"))

		entries := scanOrFatal(t, tmp)
		for _, e := range entries {
			if strings.Contains(e.RelPath, "worktrees") {
				t.Fatalf("worktree copy should be skipped, got %s", e.RelPath)
			}
		}
		found := relPathSet(entries)
		if !found[filepath.Join(".claude", "skills", "foo", "SKILL.md")] {
			t.Fatalf("expected .claude/skills/foo/SKILL.md to be indexed, got %v", relPathList(entries))
		}
	})
}

func TestScanDirMaxSize(t *testing.T) {
	t.Run("md files over 1MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		bigData := make([]byte, 1_048_577) // 1 MB + 1 byte
		for i := range bigData {
			bigData[i] = 'x'
		}
		writeFile(t, filepath.Join(tmp, "big.md"), bigData)
		writeFile(t, filepath.Join(tmp, "small.md"), []byte("# small"))

		entries := scanOrFatal(t, tmp)
		if len(entries) != 1 {
			t.Fatalf("expected 1 file (small.md only), got %d: %v", len(entries), relPathList(entries))
		}
		if entries[0].RelPath != "small.md" {
			t.Fatalf("expected small.md, got %s", entries[0].RelPath)
		}
	})

	t.Run("docx files over 10MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		writeFile(t, filepath.Join(tmp, "big.docx"), make([]byte, 10_485_761)) // 10 MB + 1 byte
		writeFile(t, filepath.Join(tmp, "small.docx"), []byte("PK"))

		found := relPathSet(scanOrFatal(t, tmp))
		if found["big.docx"] {
			t.Errorf("big.docx over 10MB should be skipped")
		}
	})

	t.Run("html and htm files over 5MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		bigData := make([]byte, 5_242_881) // 5 MB + 1 byte
		writeFile(t, filepath.Join(tmp, "big.html"), bigData)
		writeFile(t, filepath.Join(tmp, "big.htm"), bigData)
		writeFile(t, filepath.Join(tmp, "small.html"), []byte("<html></html>"))

		found := relPathSet(scanOrFatal(t, tmp))
		if found["big.html"] {
			t.Error("big.html over 5MB should be skipped")
		}
		if found["big.htm"] {
			t.Error("big.htm over 5MB should be skipped")
		}
		if !found["small.html"] {
			t.Error("small.html should be included")
		}
	})

	t.Run("pdf files over 50MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		writeFile(t, filepath.Join(tmp, "big.pdf"), make([]byte, 52_428_801)) // 50 MB + 1 byte
		writeFile(t, filepath.Join(tmp, "small.pdf"), []byte("%PDF-1.4"))

		found := relPathSet(scanOrFatal(t, tmp))
		if found["big.pdf"] {
			t.Error("big.pdf over 50MB should be skipped")
		}
		if !found["small.pdf"] {
			t.Error("small.pdf should be included")
		}
	})

	t.Run("unsupported extensions are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		for _, name := range []string{"file.txt", "file.csv", "file.xlsx", "file.png"} {
			writeFile(t, filepath.Join(tmp, name), []byte("data"))
		}
		writeFile(t, filepath.Join(tmp, "keep.md"), []byte("# ok"))

		entries := scanOrFatal(t, tmp)
		if len(entries) != 1 || entries[0].RelPath != "keep.md" {
			t.Fatalf("expected only keep.md, got %v", relPathList(entries))
		}
	})
}

func TestScanDirEmpty(t *testing.T) {
	t.Run("empty directory returns 0 files", func(t *testing.T) {
		tmp := t.TempDir()

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected 0 files, got %d", len(entries))
		}
	})
}

func TestScanDirGitignoreLeadingSlash(t *testing.T) {
	t.Run("leading-slash pattern excludes directory anchored at root", func(t *testing.T) {
		tmp := t.TempDir()

		if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("/prompts/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tmp, "prompts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "prompts", "excluded.md"), []byte("# excluded"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "included.md"), []byte("# included"), 0o644); err != nil {
			t.Fatal(err)
		}

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		found := map[string]bool{}
		for _, e := range entries {
			found[e.RelPath] = true
		}
		if found[filepath.Join("prompts", "excluded.md")] {
			t.Error("prompts/excluded.md should be excluded by /prompts/ gitignore pattern")
		}
		if !found["included.md"] {
			t.Error("included.md should not be excluded")
		}
	})
}

func TestScanDirNestedGitignoreScoping(t *testing.T) {
	t.Run("wildcard in nested .gitignore does not exclude files outside that directory", func(t *testing.T) {
		tmp := t.TempDir()

		cacheDir := filepath.Join(tmp, "cache_dir")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Nested .gitignore with wildcard — must only apply inside cache_dir/
		if err := os.WriteFile(filepath.Join(cacheDir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheDir, "noise.md"), []byte("# noise"), 0o644); err != nil {
			t.Fatal(err)
		}

		queriesDir := filepath.Join(tmp, "queries")
		if err := os.MkdirAll(queriesDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(queriesDir, "query.md"), []byte("# query"), 0o644); err != nil {
			t.Fatal(err)
		}

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		found := map[string]bool{}
		for _, e := range entries {
			found[e.RelPath] = true
		}
		if found[filepath.Join("cache_dir", "noise.md")] {
			t.Error("cache_dir/noise.md should be excluded by nested wildcard")
		}
		if !found[filepath.Join("queries", "query.md")] {
			t.Errorf("queries/query.md should not be excluded by cache_dir/.gitignore wildcard")
		}
	})
}

func TestScanDirRelPaths(t *testing.T) {
	t.Run("all RelPath values are relative, not absolute", func(t *testing.T) {
		entries, err := ScanDir(fixtureDir(t, "project-a"))
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("expected at least 1 entry")
		}

		relPaths := make([]string, len(entries))
		for i, e := range entries {
			relPaths[i] = e.RelPath
			if filepath.IsAbs(e.RelPath) {
				t.Errorf("RelPath is absolute: %s", e.RelPath)
			}
			if strings.HasPrefix(e.RelPath, "..") {
				t.Errorf("RelPath escapes root: %s", e.RelPath)
			}
		}

		// Verify expected relative paths are present
		sort.Strings(relPaths)
		expected := []string{"README.md", "doc-a.md", "doc-b.md", "subdir/nested.md"}
		sort.Strings(expected)

		if len(relPaths) != len(expected) {
			t.Fatalf("expected paths %v, got %v", expected, relPaths)
		}
		for i := range expected {
			if relPaths[i] != expected[i] {
				t.Errorf("path[%d]: expected %s, got %s", i, expected[i], relPaths[i])
			}
		}
	})
}
