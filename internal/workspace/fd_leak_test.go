package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// openFDCount approximates the number of file descriptors currently open in this
// process: the kernel assigns the lowest-available descriptor, so a freshly
// opened fd's integer value ≈ the live open-fd count. The delta across N reindex
// cycles isolates any per-cycle leak.
func openFDCount(t *testing.T) int {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	n := int(f.Fd())
	f.Close()
	return n
}

// TestReindexDoesNotLeakFDs is the companion guard to the watcher watch-cap test
// (see internal/watcher TestWatcherBoundsWatchFdCount). The serve --workspace fd
// exhaustion (plans/serve-workspace-fd-exhaustion) was root-caused to the
// recursive watcher, NOT the reindex pipeline — measurement showed zero per-cycle
// growth. This test pins that conclusion: a per-cycle leak in the git-history
// subprocess (`git log` pipes) or the SQLite handles would, fired on every file
// change over a long-lived serve, accumulate just as dangerously. The reindex
// must run on a real git repo so it exercises CollectHistories (a non-repo
// fixture skips the entire git path and would hide a pipe leak).
func TestReindexDoesNotLeakFDs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		// Disable commit/tag signing: a contributor's machine may have hardware-key
		// signing configured, which would block on user presence in a test.
		full := append([]string{"-C", proj, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
		c := exec.Command("git", full...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	for i := 0; i < 5; i++ {
		p := filepath.Join(proj, "doc"+strconv.Itoa(i)+".md")
		if err := os.WriteFile(p, []byte("# d\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-m", "init")

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	p := w.Projects[0]

	change := func(tag string) {
		if err := os.WriteFile(filepath.Join(proj, "doc0.md"), []byte("# d\n"+tag+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Warm-up: the cold-start path (empty DB, FTS bulk-rebuild) differs from the
	// steady-state incremental reindex the watcher fires; measure the latter.
	change("warmup")
	ReindexProject(p)

	base := openFDCount(t)
	const cycles = 20
	for i := 0; i < cycles; i++ {
		change(strconv.Itoa(i))
		ReindexProject(p)
	}
	delta := openFDCount(t) - base
	t.Logf("reindex cycles=%d fd delta=%d", cycles, delta)

	// Allow a tiny slack for SQLite connection-pool churn; a real leak grows ~1+
	// fd per cycle and would blow well past this on 20 cycles.
	if delta > 5 {
		t.Fatalf("reindex leaked file descriptors: %d over %d cycles (>5 slack)", delta, cycles)
	}
}
