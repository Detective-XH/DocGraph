package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestIsRepo_NonGitDir(t *testing.T) {
	tmp := t.TempDir()
	if IsRepo(tmp) {
		t.Errorf("expected IsRepo=false for non-git dir %s", tmp)
	}
}

func TestIsRepo_GitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	cmd := exec.Command("git", "-C", tmp, "init")
	cmd.Env = []string{"GIT_CONFIG_NOSYSTEM=1", "HOME=" + tmp}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if !IsRepo(tmp) {
		t.Errorf("expected IsRepo=true for git dir %s", tmp)
	}
}

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

// mustInitGitRepo runs the four git commands needed to initialise a bare
// repository in tmp (init, user.email, user.name, commit.gpgsign=false).
// It is shared by tests that need a scratch repo.
func mustInitGitRepo(t *testing.T, tmp string) {
	t.Helper()
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "HOME=" + tmp}
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "alice@example.com"},
		{"config", "user.name", "Alice"}, {"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// assertAllWorkerRegimesMatch calls CollectHistories for each worker count in
// workers and checks that every result slot equals the serial baseline want.
// This is the core parallel-matches-serial assertion.
func assertAllWorkerRegimesMatch(t *testing.T, tmp string, relPaths []string, want []FileHistory, workers []int) {
	t.Helper()
	for _, w := range workers {
		got := CollectHistories(tmp, relPaths, w)
		if len(got) != len(want) {
			t.Fatalf("workers=%d: len(got)=%d, want %d", w, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("workers=%d slot %d: got %+v, want %+v", w, i, got[i], want[i])
			}
		}
	}
}

// TestCollectHistories_ParallelMatchesSerial proves the fan-out wrapper returns
// the exact per-file FileHistory the serial path would, in input order, across
// every worker regime (serial, parallel, oversubscribed). Files get distinct
// commit counts and the input order is unsorted with an untracked path mixed in,
// so an off-by-worker or alphabetical-ordering bug cannot accidentally pass. Run
// under -race, it also guards the disjoint-slot writes against data races.
func TestCollectHistories_ParallelMatchesSerial(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	mustInitGitRepo(t, tmp)

	day := 0
	commit := func(name, content string) {
		t.Helper()
		day++
		date := fmt.Sprintf("2024-01-%02dT00:00:00Z", day) // distinct, monotonic timestamps
		env := []string{
			"GIT_CONFIG_NOSYSTEM=1", "HOME=" + tmp,
			"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com",
			"GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=alice@example.com",
			"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date,
		}
		run := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", "edit "+name)
	}

	// Distinct histories: a.md×1, b.md×2, c.md×3.
	commit("a.md", "a1")
	commit("b.md", "b1")
	commit("c.md", "c1")
	commit("b.md", "b2")
	commit("c.md", "c2")
	commit("c.md", "c3")

	// Unsorted input + an untracked path: results must align to input order
	// (not alphabetical) and the zero-value row for the missing file must pass
	// through at its slot.
	relPaths := []string{"c.md", "a.md", "missing.md", "b.md"}
	want := make([]FileHistory, len(relPaths))
	for i, p := range relPaths {
		want[i] = CollectHistory(tmp, p)
	}
	if want[0].CommitCount != 3 || want[1].CommitCount != 1 ||
		want[2].CommitCount != 0 || want[3].CommitCount != 2 {
		t.Fatalf("setup: unexpected serial commit counts: %d %d %d %d",
			want[0].CommitCount, want[1].CommitCount, want[2].CommitCount, want[3].CommitCount)
	}

	assertAllWorkerRegimesMatch(t, tmp, relPaths, want, []int{1, 2, 8, 100})

	if got := CollectHistories(tmp, nil, 8); len(got) != 0 {
		t.Errorf("nil paths: len(got)=%d, want 0", len(got))
	}
	if got := CollectHistories(tmp, []string{"a.md"}, 8); len(got) != 1 || got[0] != want[1] {
		t.Errorf("single path: got %+v, want %+v", got, want[1])
	}
}

// TestCollectHistories_GlobalForkBoundConcurrentCallers simulates the workspace
// IndexAll scenario the package-level forkSem exists for: many goroutines each
// fan CollectHistories(workers=NumCPU) at once, the way IndexAll runs NumCPU
// projects concurrently. Without the global cap this is the NumCPU² git-child
// oversubscription that can EAGAIN/ENOMEM and silently corrupt rows; the cap is
// proven by construction (forkSem is a buffered channel of NumCPU, acquired
// around every git fork), so this test guards the two things construction does
// NOT prove: that the shared budget never deadlocks under oversubscription, and
// that concurrent collectors never corrupt each other's disjoint result slots.
// Run under -race it also flags any data race on the shared sem or slots.
func TestCollectHistories_GlobalForkBoundConcurrentCallers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()

	day := 0
	commit := func(name, content string) {
		t.Helper()
		day++
		date := fmt.Sprintf("2024-02-%02dT00:00:00Z", day)
		env := []string{
			"GIT_CONFIG_NOSYSTEM=1", "HOME=" + tmp,
			"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com",
			"GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=alice@example.com",
			"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date,
		}
		run := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", "edit "+name)
	}

	mustInitGitRepo(t, tmp)
	commit("a.md", "a1")
	commit("b.md", "b1")
	commit("b.md", "b2")

	relPaths := []string{"b.md", "missing.md", "a.md"}
	want := make([]FileHistory, len(relPaths))
	for i, p := range relPaths {
		want[i] = CollectHistory(tmp, p)
	}

	// Far more concurrent callers than NumCPU, each requesting NumCPU workers, so
	// the requested-fork total dwarfs the budget and goroutines pile up on it.
	callers := max(runtime.NumCPU()*4, 16)
	var wg sync.WaitGroup
	errs := make(chan string, callers)
	for range callers {
		wg.Go(func() {
			got := CollectHistories(tmp, relPaths, runtime.NumCPU())
			for i := range want {
				if got[i] != want[i] {
					errs <- fmt.Sprintf("slot %d: got %+v, want %+v", i, got[i], want[i])
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
