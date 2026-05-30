package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNoHistoryFlagGatesGitCollection proves the --no-history opt-out (the
// package-level noHistory, read by indexStore) skips git file_history collection,
// while the default (noHistory=false) collects it. Default-on is intentional:
// file_history is an LLM-first provenance/staleness signal surfaced inline by
// docgraph_node; --no-history is the escape hatch for users indexing a large git
// repo who don't want the cost.
func TestNoHistoryFlagGatesGitCollection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1", "HOME=" + dir,
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=alice@example.com",
		"GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z",
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "alice@example.com")
	run("config", "user.name", "Alice")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "adr.md"), []byte("# ADR-001\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "adr.md")
	run("commit", "-m", "initial ADR")

	// noHistory is a package-level var (set from the --no-history flag); restore it.
	saved := noHistory
	t.Cleanup(func() { noHistory = saved })

	// Default (--no-history OFF): history IS collected.
	noHistory = false
	st := indexPathOpts(dir, true)
	h, err := st.GetFileHistory("adr.md")
	st.Close()
	if err != nil {
		t.Fatalf("GetFileHistory (default): %v", err)
	}
	if h == nil || h.CommitCount < 1 {
		t.Fatalf("default: expected file_history with CommitCount>=1, got %+v", h)
	}

	// --no-history ON: collection skipped, no file_history row written.
	noHistory = true
	st2 := indexPathOpts(dir, true)
	h2, err := st2.GetFileHistory("adr.md")
	st2.Close()
	if err != nil {
		t.Fatalf("GetFileHistory (--no-history): %v", err)
	}
	if h2 != nil {
		t.Fatalf("--no-history: expected NO file_history row, got %+v", h2)
	}
}
