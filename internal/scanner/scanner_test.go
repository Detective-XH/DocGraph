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
	t.Run("md files over 1MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		bigData := make([]byte, 1_048_577) // 1 MB + 1 byte
		for i := range bigData {
			bigData[i] = 'x'
		}
		if err := os.WriteFile(filepath.Join(tmp, "big.md"), bigData, 0o644); err != nil {
			t.Fatal(err)
		}
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

	t.Run("docx files over 10MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		bigData := make([]byte, 10_485_761) // 10 MB + 1 byte
		if err := os.WriteFile(filepath.Join(tmp, "big.docx"), bigData, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "small.docx"), []byte("PK"), 0o644); err != nil {
			t.Fatal(err)
		}

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		for _, e := range entries {
			if e.RelPath == "big.docx" {
				t.Errorf("big.docx over 10MB should be skipped")
			}
		}
	})

	t.Run("html and htm files over 5MB are skipped", func(t *testing.T) {
		tmp := t.TempDir()

		bigData := make([]byte, 5_242_881) // 5 MB + 1 byte
		if err := os.WriteFile(filepath.Join(tmp, "big.html"), bigData, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "big.htm"), bigData, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "small.html"), []byte("<html></html>"), 0o644); err != nil {
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

		bigData := make([]byte, 52_428_801) // 50 MB + 1 byte
		if err := os.WriteFile(filepath.Join(tmp, "big.pdf"), bigData, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "small.pdf"), []byte("%PDF-1.4"), 0o644); err != nil {
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
			if err := os.WriteFile(filepath.Join(tmp, name), []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(tmp, "keep.md"), []byte("# ok"), 0o644); err != nil {
			t.Fatal(err)
		}

		entries, err := ScanDir(tmp)
		if err != nil {
			t.Fatalf("ScanDir: %v", err)
		}
		if len(entries) != 1 || entries[0].RelPath != "keep.md" {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.RelPath
			}
			t.Fatalf("expected only keep.md, got %v", names)
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
