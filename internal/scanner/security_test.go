package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Red-team / security fuzzing tests for the scanner package.
// Focus: symlink traversal, permission denied, Unicode filenames, path escapes.
// ---------------------------------------------------------------------------

func TestSymlinkFile(t *testing.T) {
	tmp := t.TempDir()

	// Create a real markdown file
	if err := os.WriteFile(filepath.Join(tmp, "real.md"), []byte("# Real\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink to /etc/hosts
	link := filepath.Join(tmp, "link.md")
	if err := os.Symlink("/etc/hosts", link); err != nil {
		t.Skip("symlink not supported:", err)
	}

	entries, err := ScanDir(tmp)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.RelPath == "link.md" {
			t.Error("symlinked file link.md should NOT appear in ScanDir results")
		}
	}

	// The real file must still be present
	found := false
	for _, e := range entries {
		if e.RelPath == "real.md" {
			found = true
		}
	}
	if !found {
		t.Error("real.md should appear in ScanDir results")
	}
}

func TestSymlinkDirectory(t *testing.T) {
	tmp := t.TempDir()

	// Create a real markdown file at root
	if err := os.WriteFile(filepath.Join(tmp, "root.md"), []byte("# Root\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory symlink pointing to /etc/
	evil := filepath.Join(tmp, "evil")
	if err := os.Symlink("/etc", evil); err != nil {
		t.Skip("symlink not supported:", err)
	}

	entries, err := ScanDir(tmp)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if filepath.IsAbs(e.Path) {
			// Verify no scanned file lives under /etc/
			abs, _ := filepath.EvalSymlinks(e.Path)
			if len(abs) > 4 && abs[:4] == "/etc" {
				t.Errorf("file from /etc/ escaped via symlink: %s", e.RelPath)
			}
		}
		if e.RelPath == "evil" || (len(e.RelPath) > 5 && e.RelPath[:5] == "evil/") {
			t.Errorf("file from symlinked directory appeared in results: %s", e.RelPath)
		}
	}

	// Root file must still be found
	found := false
	for _, e := range entries {
		if e.RelPath == "root.md" {
			found = true
		}
	}
	if !found {
		t.Error("root.md should appear in ScanDir results")
	}
}

func TestPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 does not restrict on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root — permission test is meaningless")
	}

	tmp := t.TempDir()

	// Create a subdirectory with a markdown file, then chmod 000 the directory.
	// WalkDir will receive an error when entering the directory but the scanner
	// should not panic; root-level files must still be returned.
	sub := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "secret.md"), []byte("# Secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also place a visible file at root
	if err := os.WriteFile(filepath.Join(tmp, "visible.md"), []byte("# Visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Lock the subdirectory — restore in cleanup BEFORE TempDir cleanup runs
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })

	// ScanDir may return an error for the inaccessible dir, but must not panic.
	// We accept both nil error (skip) and non-nil error (propagated).
	entries, err := ScanDir(tmp)
	_ = err // we don't assert on the error — the key is no panic

	// If entries were returned, verify no file from the locked subdir leaked
	for _, e := range entries {
		if len(e.RelPath) >= 7 && e.RelPath[:7] == "locked/" {
			t.Errorf("file from locked directory appeared: %s", e.RelPath)
		}
	}
}

func TestUnicodeFilename(t *testing.T) {
	tmp := t.TempDir()

	files := map[string]string{
		"normal.md":                        "# Normal\n",
		"READ​ME.md":                  "# Zero Width Space\n", // U+200B in name
		"café.md":               "# Combining Acute\n",  // e + combining accent
	}

	created := 0
	for name, content := range files {
		path := filepath.Join(tmp, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Logf("could not create %q: %v (skipping)", name, err)
			continue
		}
		created++
	}

	if created == 0 {
		t.Fatal("could not create any test files")
	}

	entries, err := ScanDir(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// We should find at least as many files as we created (filesystem may
	// normalize combining forms, so the exact count may vary on macOS/APFS).
	if len(entries) == 0 {
		t.Fatal("ScanDir returned 0 entries for Unicode filename test")
	}

	// Verify all returned paths are valid UTF-8 (no corruption)
	for _, e := range entries {
		if !utf8.ValidString(e.RelPath) {
			t.Errorf("invalid UTF-8 in RelPath: %q", e.RelPath)
		}
	}

	// normal.md must always be present as the control file
	foundNormal := false
	for _, e := range entries {
		if e.RelPath == "normal.md" {
			foundNormal = true
		}
	}
	if !foundNormal {
		t.Error("normal.md (control file) not found in results")
	}

	t.Logf("created %d files, ScanDir returned %d entries", created, len(entries))
	for _, e := range entries {
		t.Logf("  %q", e.RelPath)
	}
}

func TestDotDotPath(t *testing.T) {
	// Create a structure:
	//   tmp/
	//     parent/
	//       child/  (scan target as "child/../child")
	//         test.md
	//     sibling/
	//       outside.md
	tmp := t.TempDir()

	child := filepath.Join(tmp, "parent", "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "test.md"), []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sibling := filepath.Join(tmp, "parent", "sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "outside.md"), []byte("# Outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use a path with ".." that resolves to child/
	dotdotPath := filepath.Join(child, "..", "child")

	entries, err := ScanDir(dotdotPath)
	if err != nil {
		t.Fatal(err)
	}

	// Should find only test.md inside child/, not outside.md from sibling/
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.RelPath
		}
		t.Fatalf("expected 1 file, got %d: %v", len(entries), names)
	}
	if entries[0].RelPath != "test.md" {
		t.Errorf("expected test.md, got %s", entries[0].RelPath)
	}

	// RelPath must not contain ".."
	for _, e := range entries {
		if filepath.IsAbs(e.RelPath) {
			t.Errorf("RelPath is absolute: %s", e.RelPath)
		}
		if containsDotDot(e.RelPath) {
			t.Errorf("RelPath contains '..': %s", e.RelPath)
		}
	}
}

// containsDotDot checks whether a path has a ".." component.
func containsDotDot(p string) bool {
	for _, part := range filepath.SplitList(p) {
		if part == ".." {
			return true
		}
	}
	// Also check slash-separated components directly
	for p != "" {
		var seg string
		if i := len(p); i > 0 {
			seg = p
			if idx := findSep(p); idx >= 0 {
				seg = p[:idx]
				p = p[idx+1:]
			} else {
				p = ""
			}
		}
		if seg == ".." {
			return true
		}
	}
	return false
}

func findSep(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' || s[i] == filepath.Separator {
			return i
		}
	}
	return -1
}
