package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/install"
)

// TestClaudeInstalled_empty verifies that an empty results slice returns false.
func TestClaudeInstalled_empty(t *testing.T) {
	if claudeInstalled(nil) {
		t.Error("expected false for nil results, got true")
	}
	if claudeInstalled([]install.Result{}) {
		t.Error("expected false for empty results, got true")
	}
}

// TestClaudeInstalled_nonClaude verifies that results containing only non-claude
// clients return false.
func TestClaudeInstalled_nonClaude(t *testing.T) {
	results := []install.Result{
		{Client: "codex", Path: "/some/path"},
		{Client: "hermes", Path: "/other/path"},
		{Client: "opencode", Path: "/third/path"},
	}
	if claudeInstalled(results) {
		t.Error("expected false for non-claude results, got true")
	}
}

// TestClaudeInstalled_claudeNotOnPath verifies that a result with Client="claude"
// returns false when the claude binary is not on PATH.
// This test zeroes PATH so exec.LookPath("claude") reliably fails on any machine,
// including developer machines that have claude installed.
func TestClaudeInstalled_claudeNotOnPath(t *testing.T) {
	t.Setenv("PATH", "")
	results := []install.Result{
		{Client: "claude", Path: "/some/path"},
	}
	if claudeInstalled(results) {
		t.Error("expected false when claude is not on PATH, got true")
	}
}

// TestInstallSkills_fresh verifies that a fresh install (overwrite=false) creates
// the expected SKILL.md file with correct frontmatter content.
func TestInstallSkills_fresh(t *testing.T) {
	root := t.TempDir()

	if err := installSkills(root, false); err != nil {
		t.Fatalf("installSkills returned error: %v", err)
	}

	skillFile := filepath.Join(root, ".claude", "skills", "docgraph-drift-audit", "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("expected SKILL.md to exist at %s: %v", skillFile, err)
	}

	content := string(data)
	if !strings.Contains(content, "name: docgraph-drift-audit") {
		t.Errorf("SKILL.md missing expected frontmatter 'name: docgraph-drift-audit'; got:\n%s", content[:min(200, len(content))])
	}
	if !strings.Contains(content, "---") {
		t.Errorf("SKILL.md missing YAML frontmatter delimiters")
	}
}

// TestInstallSkills_skipExisting verifies that a second install with overwrite=false
// does not overwrite a manually modified SKILL.md.
func TestInstallSkills_skipExisting(t *testing.T) {
	root := t.TempDir()

	// First install.
	if err := installSkills(root, false); err != nil {
		t.Fatalf("first installSkills returned error: %v", err)
	}

	skillFile := filepath.Join(root, ".claude", "skills", "docgraph-drift-audit", "SKILL.md")

	// Overwrite with sentinel content.
	sentinel := "SENTINEL CONTENT — must not be overwritten"
	if err := os.WriteFile(skillFile, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}

	// Second install — should skip the existing directory.
	if err := installSkills(root, false); err != nil {
		t.Fatalf("second installSkills returned error: %v", err)
	}

	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("failed to read SKILL.md after second install: %v", err)
	}
	if string(data) != sentinel {
		t.Errorf("expected sentinel content to be preserved, got:\n%s", string(data))
	}
}

// TestInstallSkills_overwrite verifies that installSkills with overwrite=true
// replaces a manually modified SKILL.md with the original embedded content.
func TestInstallSkills_overwrite(t *testing.T) {
	root := t.TempDir()

	// First install.
	if err := installSkills(root, false); err != nil {
		t.Fatalf("first installSkills returned error: %v", err)
	}

	skillFile := filepath.Join(root, ".claude", "skills", "docgraph-drift-audit", "SKILL.md")

	// Overwrite with sentinel content.
	sentinel := "SENTINEL CONTENT — must be replaced on overwrite"
	if err := os.WriteFile(skillFile, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}

	// Second install with overwrite=true — must replace the sentinel.
	if err := installSkills(root, true); err != nil {
		t.Fatalf("installSkills(overwrite=true) returned error: %v", err)
	}

	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("failed to read SKILL.md after overwrite install: %v", err)
	}

	// Read original from embedded FS for comparison.
	original, err := fs.ReadFile(skillsFS, "skills/docgraph-drift-audit/SKILL.md")
	if err != nil {
		t.Fatalf("failed to read embedded SKILL.md: %v", err)
	}

	if string(data) == sentinel {
		t.Error("sentinel content was not replaced by overwrite install")
	}
	if string(data) != string(original) {
		t.Errorf("overwritten SKILL.md does not match embedded original\nwant:\n%s\ngot:\n%s",
			string(original)[:min(200, len(original))], string(data)[:min(200, len(data))])
	}
}

// min returns the smaller of two ints (helper for safe string slicing in error messages).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
