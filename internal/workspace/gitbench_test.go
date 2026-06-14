package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

// TestWorkspaceGitHistoryBench measures cold-start IndexAll wall time on a
// workspace whose child projects are REAL git repos with REAL, DEEP per-file
// commit history. It is the ship-vs-reject gate evidence for parallelizing the
// per-file git-history collection inside the multi-project IndexAll path
// (gate = 15% wall improvement, parallel vs serial).
//
// This file deliberately does NOT toggle serial/parallel. The two arms are
// produced by COMPILING THIS SAME FILE against two code versions (serial HEAD
// via a git worktree, parallel branch) and running each against a SHARED
// on-disk corpus (DG_WS_GIT_BENCH_CORPUS). The corpus and the per-project
// file_history sha (FHSHA) are the contract between the two arms: identical
// corpus in, identical FHSHA out — only the wall time differs.
//
// Gated behind DG_WS_GIT_BENCH so it never runs in the normal suite. Run
// WITHOUT -race (the heap sampler races benignly with IndexAll by design).
//
//	DG_WS_GIT_BENCH=1 \
//	DG_WS_GIT_BENCH_CORPUS=/tmp/dg-gitbench \
//	go test -run TestWorkspaceGitHistoryBench -count=1 -timeout 1200s -v ./internal/workspace/
func TestWorkspaceGitHistoryBench(t *testing.T) {
	if os.Getenv("DG_WS_GIT_BENCH") == "" {
		t.Skip("set DG_WS_GIT_BENCH=1 to run the workspace git-history IndexAll benchmark")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Corpus shape. The git projects carry DEEP per-file history; the non-git
	// project proves the gitEnabled=false path stays inert (collects nothing).
	const (
		gitProjects = 4   // real git repos
		perGitProj  = 120 // .md docs per git repo
		nonGitDocs  = 120 // .md docs in the single non-git project
		// Per-file history depth: each git doc is committed in exactly `sweeps`
		// commits (one per sweep; see genGitProject). Tens of commits reachable
		// per file via `git log --follow` — depth is the whole point of the gate.
		sweeps = 30
	)
	const iters = 5 // iter 0 is dropped as warmup

	root, gitProjectNames, gitRelPaths, nonGitRelPaths, nonGitName :=
		setupGitBenchCorpus(t, gitProjects, perGitProj, nonGitDocs, sweeps)

	// Measurement loop: cold-start each iter (wipe every .docgraph), then time
	// IndexAll. A heap sampler runs alongside IndexAll to capture peak HeapInuse.
	times := make([]time.Duration, 0, iters)
	var peakHeap uint64
	for i := range iters {
		clearWorkspaceDocgraph(t, root)
		w, err := Open(root)
		if err != nil {
			t.Fatalf("Open(%s): %v", root, err)
		}
		w.NoGitignore = true
		w.SimilarityThreshold = 0.25

		stop := make(chan struct{})
		done := make(chan struct{})
		var sampledPeak uint64
		go sampleHeapPeak(stop, done, &sampledPeak)

		start := time.Now()
		err = w.IndexAll()
		d := time.Since(start)
		close(stop)
		<-done // wait for the sampler to settle before reading the peak
		if err != nil {
			w.Close()
			t.Fatalf("IndexAll iter %d: %v", i, err)
		}

		if sampledPeak > peakHeap {
			peakHeap = sampledPeak
		}

		// On the LAST iter the DB is populated — verify the file_history
		// contract (FHSHA per git project, INERT for the non-git project)
		// against the OPEN workspace before closing.
		if i == iters-1 {
			verifyFileHistoryContract(t, w, gitProjectNames, gitRelPaths, nonGitName, nonGitRelPaths)
		}

		w.Close()
		times = append(times, d)
		t.Logf("iter %d cold-start IndexAll: %v (heap peak so far %s)", i, d, humanBytes(peakHeap))
	}

	// Drop iter 0 as warmup; mean + min the rest.
	measured := times[1:]
	if len(measured) == 0 {
		t.Fatalf("no measured iterations (iters=%d)", iters)
	}
	mean, minD := summarizeTimings(measured)
	totalDocs := gitProjects*perGitProj + nonGitDocs
	t.Logf("RESULT git-history cold-start IndexAll mean=%v min=%v peakHeapInuse=%s "+
		"(%d measured iters, %d git projects x %d docs x %d sweeps + non-git %d docs = %d docs total)",
		mean, minD, humanBytes(peakHeap),
		len(measured), gitProjects, perGitProj, sweeps, nonGitDocs, totalDocs)
}

// setupGitBenchCorpus resolves or creates the bench corpus root and populates
// it with git projects and a plain-docs project when the sentinel is absent.
// Returns the root dir, git project names, git rel-paths, non-git rel-paths,
// and the non-git project name.
func setupGitBenchCorpus(t *testing.T, gitProjects, perGitProj, nonGitDocs, sweeps int) (
	root string, gitProjectNames, gitRelPaths, nonGitRelPaths []string, nonGitName string,
) {
	t.Helper()

	// Persistent shared corpus (two separately-compiled binaries measure
	// byte-identical repos) vs single-binary temp mode.
	root = os.Getenv("DG_WS_GIT_BENCH_CORPUS")
	if root == "" {
		root = t.TempDir()
	} else {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir corpus root %s: %v", root, err)
		}
	}

	// gitProjectNames are the project (subdir) names that are real git repos.
	// nonGitName is the single plain-docs project.
	gitProjectNames = make([]string, gitProjects)
	for i := range gitProjectNames {
		gitProjectNames[i] = fmt.Sprintf("gitproj%02d", i)
	}
	nonGitName = "plaindocs"

	// relPaths each project will contain (flat layout → relPath == filename).
	gitRelPaths = make([]string, perGitProj)
	for f := range gitRelPaths {
		gitRelPaths[f] = fmt.Sprintf("doc_%03d.md", f)
	}
	nonGitRelPaths = make([]string, nonGitDocs)
	for f := range nonGitRelPaths {
		nonGitRelPaths[f] = fmt.Sprintf("doc_%03d.md", f)
	}

	sentinel := filepath.Join(root, ".generated")
	if _, err := os.Stat(sentinel); err != nil {
		// Generate (temp mode, or persistent mode on first run).
		t.Logf("generating git-history corpus at %s (%d git projects x %d docs x %d sweeps, + non-git %d docs)",
			root, gitProjects, perGitProj, sweeps, nonGitDocs)
		genStart := time.Now()
		for _, name := range gitProjectNames {
			genGitProject(t, filepath.Join(root, name), perGitProj, sweeps)
		}
		genPlainProject(t, filepath.Join(root, nonGitName), nonGitDocs)
		if err := os.WriteFile(sentinel, []byte("generated\n"), 0o644); err != nil {
			t.Fatalf("write sentinel: %v", err)
		}
		t.Logf("corpus generated in %v", time.Since(genStart))
	} else {
		t.Logf("reusing existing corpus at %s (sentinel present)", root)
	}

	return root, gitProjectNames, gitRelPaths, nonGitRelPaths, nonGitName
}

// verifyFileHistoryContract checks the file_history contract on the last benchmark
// iteration: each git project must have non-zero FHSHA rows, and the non-git
// project must have zero history rows (proving gitEnabled=false stays inert).
func verifyFileHistoryContract(t *testing.T, w *Workspace,
	gitProjectNames, gitRelPaths []string,
	nonGitName string, nonGitRelPaths []string,
) {
	t.Helper()
	for _, name := range gitProjectNames {
		p := w.FindProject(name)
		if p == nil {
			t.Errorf("git project %q not found in workspace", name)
			continue
		}
		sha, rows := fileHistorySHA(t, p, gitRelPaths)
		if rows == 0 {
			t.Errorf("FHSHA %s: zero history rows — git history was not collected", name)
		}
		t.Logf("FHSHA %s %s (%d rows)", name, sha, rows)
	}
	// Inert-path proof: the non-git project must have NO history rows.
	pn := w.FindProject(nonGitName)
	if pn == nil {
		t.Errorf("non-git project %q not found in workspace", nonGitName)
		return
	}
	bad := 0
	for _, rp := range nonGitRelPaths {
		h, err := pn.Store.GetFileHistory(rp)
		if err != nil {
			t.Errorf("GetFileHistory %s/%s: %v", nonGitName, rp, err)
			break
		}
		if h != nil {
			bad++
		}
	}
	if bad != 0 {
		t.Errorf("INERT %s FAIL: %d/%d files have a history row (gitEnabled should be false on a non-git tree)",
			nonGitName, bad, len(nonGitRelPaths))
	} else {
		t.Logf("INERT %s OK (%d files, 0 history rows)", nonGitName, len(nonGitRelPaths))
	}
}

// summarizeTimings returns the mean and minimum duration from a slice of
// measured iteration times (caller has already dropped the warmup iter).
func summarizeTimings(measured []time.Duration) (mean, min time.Duration) {
	min = measured[0]
	var sum time.Duration
	for _, d := range measured {
		sum += d
		if d < min {
			min = d
		}
	}
	return sum / time.Duration(len(measured)), min
}

// fileHistorySHA folds the stored file_history rows for relPaths (sorted) into a
// single sha256 over a fixed textual format. It depends ONLY on stored data, so
// the serial and parallel binaries must produce a byte-identical digest. Returns
// the hex digest and the count of non-nil rows folded.
func fileHistorySHA(t *testing.T, p *Project, relPaths []string) (string, int) {
	t.Helper()
	sorted := append([]string(nil), relPaths...)
	sort.Strings(sorted)
	h := sha256.New()
	rows := 0
	for _, rp := range sorted {
		fh, err := p.Store.GetFileHistory(rp)
		if err != nil {
			t.Fatalf("GetFileHistory %s/%s: %v", p.Name, rp, err)
		}
		if fh == nil {
			continue
		}
		rows++
		// Fixed, deterministic field order; ints/timestamps as decimal.
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%d\x00%d\x00%s\x00%s\n",
			fh.Path, fh.CommitCount, fh.FirstCommitAt, fh.LastCommitAt,
			fh.AuthorCount, fh.LastAuthor, fh.LastSubject)
	}
	return hex.EncodeToString(h.Sum(nil)), rows
}

// sampleHeapPeak polls runtime.ReadMemStats every ~50ms and records the maximum
// observed HeapInuse into *peak until stop is closed, then closes done so the
// caller can read *peak after the final sample lands. runtime.ReadMemStats
// stops the world, so this perturbs the measured wall time slightly — acceptable
// for a gated, relative (serial-vs-parallel) bench run without -race.
func sampleHeapPeak(stop <-chan struct{}, done chan<- struct{}, peak *uint64) {
	defer close(done)
	var ms runtime.MemStats
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			runtime.ReadMemStats(&ms)
			if cur := atomic.LoadUint64(peak); ms.HeapInuse > cur {
				atomic.StoreUint64(peak, ms.HeapInuse)
			}
		}
	}
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// genGitProject creates a real git repo at dir with `perProj` .md docs, each
// committed in exactly `sweeps` commits (so `git log --follow -- <doc>` returns
// `sweeps` commits per file). One commit per sweep rewrites the first half of
// the docs, the next commit rewrites the second half: two commits tile all docs
// once per sweep, so every file accumulates exactly `sweeps` commits. Commit
// authors rotate over 3 identities (so author_count > 1) and commit dates are
// strictly monotonic, making both the corpus and the resulting file_history
// rows fully reproducible across separately-compiled binaries.
func genGitProject(t *testing.T, dir string, perProj, sweeps int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Isolated HOME so the repo never sees the user's ~/.gitconfig.
	home := t.TempDir()
	baseEnv := []string{"GIT_CONFIG_NOSYSTEM=1", "HOME=" + home}
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(baseEnv, "init")
	run(baseEnv, "config", "user.email", "gen@example.com")
	run(baseEnv, "config", "user.name", "Generator")
	run(baseEnv, "config", "commit.gpgsign", "false")

	authors := []struct{ name, email string }{
		{"Alice", "alice@example.com"},
		{"Bob", "bob@example.com"},
		{"Carol", "carol@example.com"},
	}
	// Deterministic monotonic timestamps: base epoch + commitIdx minutes.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	commitIdx := 0
	half := (perProj + 1) / 2

	writeFile := func(f, sweep int) {
		// Body changes every touch (round) so `git commit` always has a diff.
		// Final on-disk content is whatever the last sweep wrote; FHSHA depends
		// only on file_history rows, not on this body.
		neigh := fmt.Sprintf("doc_%03d.md", (f+1)%perProj)
		body := fmt.Sprintf("---\ntitle: Doc %d\nstatus: %s\ntags: [t%d, a%d]\n---\n"+
			"# Doc %d (rev %d)\n\nRelated: [n](%s).\n\n## Background\n\n"+
			"%s revision %d of document %d in this git project.\n\n## Details\n\n"+
			"Sweep %d touched this file with deterministic content.\n",
			f, []string{"draft", "review", "approved"}[f%3], f%12, f%7,
			f, sweep, neigh, gitBodyFiller, sweep, f, sweep)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("doc_%03d.md", f)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	commit := func(lo, hi, sweep int) {
		for f := lo; f < hi; f++ {
			writeFile(f, sweep)
		}
		date := base.Add(time.Duration(commitIdx) * time.Minute).Format("2006-01-02T15:04:05Z07:00")
		a := authors[commitIdx%len(authors)]
		env := []string{
			"GIT_CONFIG_NOSYSTEM=1", "HOME=" + home,
			"GIT_AUTHOR_NAME=" + a.name, "GIT_AUTHOR_EMAIL=" + a.email,
			"GIT_COMMITTER_NAME=" + a.name, "GIT_COMMITTER_EMAIL=" + a.email,
			"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date,
		}
		run(env, "add", "-A")
		run(env, "commit", "-m", fmt.Sprintf("sweep %d rows %d-%d", sweep, lo, hi-1))
		commitIdx++
	}

	for s := range sweeps {
		// Two commits per sweep tile all docs once → +1 commit per file per sweep.
		commit(0, half, s)
		commit(half, perProj, s)
	}
}

// gitBodyFiller mirrors the markdown body shape from genWorkspaceCorpus so the
// parser/FTS/similarity work is comparable to the non-git project's docs.
const gitBodyFiller = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor " +
	"incididunt ut labore et dolore magna aliqua ut enim ad minim veniam quis nostrud exercitation ullamco laboris"

// genPlainProject writes `perProj` .md docs into dir with NO git init, mirroring
// the genWorkspaceCorpus body shape. On this tree git.IsRepo is false, so the
// indexer's gitEnabled probe stays false and collects no history (proven by the
// INERT assertion above).
func genPlainProject(t *testing.T, dir string, perProj int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statuses := []string{"draft", "review", "approved"}
	for f := range perProj {
		neigh := fmt.Sprintf("doc_%03d.md", (f+1)%perProj)
		body := fmt.Sprintf("---\ntitle: Plain Doc %d\nstatus: %s\ntags: [t%d, a%d]\n---\n"+
			"# Plain Doc %d\n\n%s\n\nRelated: [n](%s).\n\n## Background\n\n%s\n\n## Details\n\n%s\n",
			f, statuses[f%3], f%12, f%7, f, gitBodyFiller, neigh, gitBodyFiller, gitBodyFiller)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("doc_%03d.md", f)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
