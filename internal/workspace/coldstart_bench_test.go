package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestColdStartWorkspaceIndexBench measures the wall time of a cold-start
// (empty per-project DB) workspace IndexAll — the only path where the
// indexProjectOpts per-file deletes run over empty tables as no-ops. Gated
// behind DG_WS_BENCH so it never runs in the normal suite. Temporary
// measurement harness for the §A lever (delete-skip on cold-start); not a
// shipped test.
func TestColdStartWorkspaceIndexBench(t *testing.T) {
	if os.Getenv("DG_WS_BENCH") == "" {
		t.Skip("set DG_WS_BENCH=1 to run the cold-start workspace IndexAll benchmark")
	}
	// Projects index in parallel (bounded NumCPU), so cold-start wall ≈ the
	// largest project's latency. Use fewer, larger projects so the per-project
	// critical path (where the no-op deletes accumulate) is substantial.
	const nProj, perProj = 4, 1500 // 6000 files; wall ≈ a 1500-file project
	root := t.TempDir()
	genWorkspaceCorpus(t, root, nProj, perProj)

	const iters = 4
	times := make([]time.Duration, 0, iters)
	for i := range iters {
		clearWorkspaceDocgraph(t, root)
		w, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		w.NoGitignore = true
		w.SimilarityThreshold = 0.25
		start := time.Now()
		if err := w.IndexAll(); err != nil {
			t.Fatal(err)
		}
		d := time.Since(start)
		w.Close()
		times = append(times, d)
		t.Logf("iter %d cold-start IndexAll: %v", i+1, d)
	}
	var sum, min time.Duration
	min = times[0]
	for _, d := range times {
		sum += d
		if d < min {
			min = d
		}
	}
	t.Logf("RESULT cold-start IndexAll mean=%v min=%v (%d iters, %d projects x %d files = %d)",
		sum/time.Duration(len(times)), min, iters, nProj, perProj, nProj*perProj)
}

func genWorkspaceCorpus(t *testing.T, root string, nProj, perProj int) {
	t.Helper()
	const w = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua ut enim ad minim veniam quis nostrud exercitation ullamco laboris"
	statuses := []string{"draft", "review", "approved"}
	for p := range nProj {
		proj := filepath.Join(root, fmt.Sprintf("proj%02d", p))
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range perProj {
			neigh := fmt.Sprintf("doc_%03d.md", (f+1)%perProj)
			body := fmt.Sprintf("---\ntitle: Doc %d-%d\nstatus: %s\ntags: [t%d, a%d]\n---\n"+
				"# Doc %d\n\n%s\n\nRelated: [n](%s).\n\n## Background\n\n%s\n\n## Details\n\n%s\n",
				p, f, statuses[f%3], f%12, f%7, f, w, neigh, w, w)
			if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("doc_%03d.md", f)), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func clearWorkspaceDocgraph(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := os.RemoveAll(filepath.Join(root, e.Name(), ".docgraph")); err != nil {
				t.Fatal(err)
			}
		}
	}
}
