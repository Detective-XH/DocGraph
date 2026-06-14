package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// gitRun runs a git subcommand inside dir with signing disabled (a contributor's
// hardware-key setup must not block on user presence) and an isolated identity.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
	c := exec.Command("git", full...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestIndexProjectOpts_GitAndPlainCharacterization is the guardrail-8
// characterization lock for the PR3 indexProjectOpts decomposition. It MUST pass
// byte-for-byte before and after the refactor.
//
// It seeds a 2-project workspace and asserts concrete index OUTPUT on BOTH branches
// of indexProjectOpts that the refactor splits apart:
//
//   - git-proj is a REAL git repo (gitEnabled=true) → exercises the buffered git
//     path (batch → CollectHistories → writeOne(res, histories[idx])) being
//     extracted into projectFlushGitBatch. alpha.md and beta.md are committed in
//     SEPARATE commits with distinct subjects, so asserting each file's
//     FileHistory.LastSubject pins the histories[idx]↔batch[idx] alignment — the
//     least-covered region in the existing suite (coldstart_fts is non-git only;
//     fd_leak asserts fd counts, not index output).
//   - plain-proj is non-git (gitEnabled=false) → exercises the streaming writeOne
//     path, with an intra-project wikilink to lock resolver.Resolve output.
func TestIndexProjectOpts_GitAndPlainCharacterization(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitProj := filepath.Join(root, "git-proj")
	plainProj := filepath.Join(root, "plain-proj")

	// --- git-proj: real repo; two docs in distinct commits (alpha→beta wikilink) ---
	writeWSFile(t, filepath.Join(gitProj, "alpha.md"),
		"---\nstatus: approved\n---\n# Alpha\n\nBody mentions uniquealphaterm. See [[beta]].\n")
	gitRun(t, gitProj, "init")
	gitRun(t, gitProj, "add", "-A")
	gitRun(t, gitProj, "commit", "-m", "add alpha")
	writeWSFile(t, filepath.Join(gitProj, "beta.md"),
		"# Beta\n\nFiller body with uniquebetaterm.\n")
	gitRun(t, gitProj, "add", "-A")
	gitRun(t, gitProj, "commit", "-m", "add beta")

	// --- plain-proj: non-git; intra-project wikilink gamma→delta (resolves) ---
	writeWSFile(t, filepath.Join(plainProj, "gamma.md"),
		"# Gamma\n\nBody mentions uniquegammaterm. See [[delta]].\n")
	writeWSFile(t, filepath.Join(plainProj, "delta.md"),
		"# Delta\n\nFiller body with uniquedeltaterm.\n")

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w.NoGitignore = true
	w.SimilarityThreshold = 0.99 // suppress similar_to edges so counts stay deterministic

	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}

	gp := w.FindProject("git-proj")
	if gp == nil {
		t.Fatal("git-proj not opened")
	}
	pp := w.FindProject("plain-proj")
	if pp == nil {
		t.Fatal("plain-proj not opened")
	}

	gStats, err := gp.Store.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	pStats, err := pp.Store.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	// DISCOVERY (tightened to exact assertions below once observed on unmodified code).
	t.Logf("git-proj   stats: files=%d nodes=%d edges=%d unresolved=%d",
		gStats.FileCount, gStats.NodeCount, gStats.EdgeCount, gStats.UnresolvedCount)
	t.Logf("plain-proj stats: files=%d nodes=%d edges=%d unresolved=%d",
		pStats.FileCount, pStats.NodeCount, pStats.EdgeCount, pStats.UnresolvedCount)

	// --- Output counts (exact: locks node/edge/unresolved production on both paths) ---
	assertStat(t, "git-proj files", gStats.FileCount, GIT_FILES)
	assertStat(t, "git-proj nodes", gStats.NodeCount, GIT_NODES)
	assertStat(t, "git-proj edges", gStats.EdgeCount, GIT_EDGES)
	assertStat(t, "git-proj unresolved", gStats.UnresolvedCount, GIT_UNRESOLVED)
	assertStat(t, "plain-proj files", pStats.FileCount, PLAIN_FILES)
	assertStat(t, "plain-proj nodes", pStats.NodeCount, PLAIN_NODES)
	assertStat(t, "plain-proj edges", pStats.EdgeCount, PLAIN_EDGES)
	assertStat(t, "plain-proj unresolved", pStats.UnresolvedCount, PLAIN_UNRESOLVED)

	// --- git path: per-file history alignment (the projectFlushGitBatch guard) ---
	// alpha.md's only touching commit is "add alpha"; beta.md's is "add beta". A
	// histories[idx]↔batch[idx] misalignment would cross these subjects.
	assertFileHistory(t, gp.Store, "alpha.md", "add alpha", 1)
	assertFileHistory(t, gp.Store, "beta.md", "add beta", 1)

	// --- streaming path must write NO history (non-git project) ---
	assertNoHistory(t, pp.Store, "gamma.md")

	// --- FTS: every unique term must be searchable workspace-wide (rebuild ran) ---
	for _, term := range []string{"uniquealphaterm", "uniquebetaterm", "uniquegammaterm", "uniquedeltaterm"} {
		if hits := wsSearchCount(t, w, term); hits == 0 {
			t.Errorf("workspace search %q = 0 hits; want >0 (FTS not populated)", term)
		}
	}
}

// assertStat fails the test if got != want, naming the metric.
func assertStat(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d; want %d", name, got, want)
	}
}

// assertFileHistory asserts the file at path carries a git history row with the given
// last-commit subject and commit count (the git write path).
func assertFileHistory(t *testing.T, st *store.Store, path, wantSubject string, wantCommits int) {
	t.Helper()
	h, err := st.GetFileHistory(path)
	if err != nil {
		t.Fatalf("GetFileHistory %s: %v", path, err)
	}
	if h == nil {
		t.Fatalf("%s has no git history (git path did not write it)", path)
	}
	if h.LastSubject != wantSubject {
		t.Errorf("%s LastSubject = %q; want %q (history misaligned)", path, h.LastSubject, wantSubject)
	}
	if h.CommitCount != wantCommits {
		t.Errorf("%s CommitCount = %d; want %d", path, h.CommitCount, wantCommits)
	}
}

// assertNoHistory asserts the file at path has NO git history row (the streaming
// non-git write path must not synthesize one).
func assertNoHistory(t *testing.T, st *store.Store, path string) {
	t.Helper()
	h, err := st.GetFileHistory(path)
	if err != nil {
		t.Fatalf("GetFileHistory %s: %v", path, err)
	}
	if h != nil {
		t.Errorf("%s unexpectedly has git history: %#v", path, h)
	}
}

// Expected index-output counts, captured from the UNMODIFIED indexProjectOpts and
// then asserted to survive the PR3 decomposition unchanged. See the test doc.
const (
	// git-proj: alpha.md + beta.md → 2 document + 2 heading nodes;
	// edges = 2 contains + 1 alpha→beta wikilink.
	GIT_FILES      = 2
	GIT_NODES      = 4
	GIT_EDGES      = 3
	GIT_UNRESOLVED = 0
	// plain-proj: gamma.md + delta.md → same shape (gamma→delta wikilink).
	PLAIN_FILES      = 2
	PLAIN_NODES      = 4
	PLAIN_EDGES      = 3
	PLAIN_UNRESOLVED = 0
)
