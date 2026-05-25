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

		nmDir := filepath.Join(tmp, "node_modules")
		if err := os.MkdirAll(nmDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nmDir, "test.md"), []byte("# hidden"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Also place a visible file at the root
		if err := os.WriteFile(filepath.Join(tmp, "visible.md"), []byte("# visible"), 0o644); err != nil {
			t.Fatal(err)
		}

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) != 1 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.RelPath
			}
			t.Fatalf("expected 1 file (visible.md only), got %d: %v", len(entries), names)
		}
		if entries[0].RelPath != "visible.md" {
			t.Fatalf("expected visible.md, got %s", entries[0].RelPath)
		}
	})
}

func TestScanDirMaxSize(t *testing.T) {
	t.Run("files over 1MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		// Create a file larger than maxFileSize (1_048_576)
		bigData := make([]byte, 1_048_577)
		for i := range bigData {
			bigData[i] = 'x'
		}
		if err := os.WriteFile(filepath.Join(tmp, "big.md"), bigData, 0o644); err != nil {
			t.Fatal(err)
		}
		// Also place a small file
		if err := os.WriteFile(filepath.Join(tmp, "small.md"), []byte("# small"), 0o644); err != nil {
			t.Fatal(err)
		}

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) != 1 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.RelPath
			}
			t.Fatalf("expected 1 file (small.md only), got %d: %v", len(entries), names)
		}
		if entries[0].RelPath != "small.md" {
			t.Fatalf("expected small.md, got %s", entries[0].RelPath)
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
