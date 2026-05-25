package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCollectHistory_NonGitDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "doc.md"), []byte("# Hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := CollectHistory(tmp, "doc.md")
	if h.CommitCount != 0 {
		t.Errorf("expected 0 commits in non-git dir, got %d", h.CommitCount)
	}
	if h.Path != "doc.md" {
		t.Errorf("expected path=doc.md, got %s", h.Path)
	}
}

func TestCollectHistory_UntrackedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + tmp,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	// Write file but do NOT add/commit it.
	if err := os.WriteFile(filepath.Join(tmp, "untracked.md"), []byte("# Untracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := CollectHistory(tmp, "untracked.md")
	if h.CommitCount != 0 {
		t.Errorf("expected 0 commits for untracked file, got %d", h.CommitCount)
	}
}

func TestCollectHistory_CommittedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()

	// GIT_CONFIG_NOSYSTEM prevents loading system/global git config (e.g. commit signing).
	baseEnv := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + tmp, // isolate from user's ~/.gitconfig
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=alice@example.com",
		"GIT_AUTHOR_DATE=2024-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2024-01-01T00:00:00Z",
	}
	run := func(extraEnv []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		cmd.Env = extraEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run(baseEnv, "init")
	run(baseEnv, "config", "user.email", "alice@example.com")
	run(baseEnv, "config", "user.name", "Alice")
	run(baseEnv, "config", "commit.gpgsign", "false")

	// First commit.
	if err := os.WriteFile(filepath.Join(tmp, "adr.md"), []byte("# ADR-001"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(baseEnv, "add", "adr.md")
	run(baseEnv, "commit", "-m", "initial ADR")

	// Second commit by Bob.
	env2 := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + tmp,
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com",
		"GIT_COMMITTER_NAME=Bob", "GIT_COMMITTER_EMAIL=bob@example.com",
		"GIT_AUTHOR_DATE=2024-02-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2024-02-01T00:00:00Z",
	}
	if err := os.WriteFile(filepath.Join(tmp, "adr.md"), []byte("# ADR-001 amended"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(env2, "add", "adr.md")
	run(env2, "commit", "-m", "amend ADR")

	h := CollectHistory(tmp, "adr.md")
	if h.CommitCount != 2 {
		t.Errorf("expected 2 commits, got %d", h.CommitCount)
	}
	if h.AuthorCount != 2 {
		t.Errorf("expected 2 authors, got %d", h.AuthorCount)
	}
	if h.LastSubject != "amend ADR" {
		t.Errorf("expected last subject %q, got %q", "amend ADR", h.LastSubject)
	}
	if h.CommitCount > 0 && h.FirstCommitAt >= h.LastCommitAt {
		t.Errorf("first commit should be before last: first=%d last=%d", h.FirstCommitAt, h.LastCommitAt)
	}
	if h.Path != "adr.md" {
		t.Errorf("expected path=adr.md, got %s", h.Path)
	}
}
