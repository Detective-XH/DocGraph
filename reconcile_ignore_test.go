package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/scanner"
)

// TestContainsIgnoreRuleFile pins the trigger that makes the watcher run the
// ignore-aware reconcile when an ignore-rule file is among the changed paths.
func TestContainsIgnoreRuleFile(t *testing.T) {
	if !containsIgnoreRuleFile([]string{"a.md", ".docgraphignore"}) {
		t.Error("should detect .docgraphignore")
	}
	if !containsIgnoreRuleFile([]string{"sub/dir/.gitignore"}) {
		t.Error("should detect a nested .gitignore by basename")
	}
	if containsIgnoreRuleFile([]string{"a.md", "b.pdf"}) {
		t.Error("should not flag ordinary files")
	}
}

// TestReconcileDeletedFiles_PrunesNewlyIgnored verifies the ignore-aware reconcile:
// a file still present on disk but newly matched by a .docgraphignore rule is
// pruned, while non-matching files survive. This is the present-but-ignored gap the
// deletion-only reconcile (and the per-file scan loop) cannot close.
func TestReconcileDeletedFiles_PrunesNewlyIgnored(t *testing.T) {
	dir, st := openPruneTestStore(t)
	indexPruneTestFile(t, dir, st, "keep1.md", "# K1\n\nkeeper one.\n")
	indexPruneTestFile(t, dir, st, "keep2.md", "# K2\n\nkeeper two.\n")
	indexPruneTestFile(t, dir, st, "drop.md", "# Drop\n\nwill be ignored.\n")

	// Add the exclusion AFTER indexing; the file stays on disk.
	if err := os.WriteFile(filepath.Join(dir, ".docgraphignore"), []byte("drop.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := scanner.NewIgnoreMatcher(dir, scanner.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if pruned := reconcileDeletedFiles(dir, st, m); pruned != 1 {
		t.Fatalf("expected 1 newly-ignored file pruned, got %d", pruned)
	}
	if n := nodeCountSQL(t, dir, "drop.md"); n != 0 {
		t.Errorf("drop.md should be pruned, got %d nodes", n)
	}
	if n := nodeCountSQL(t, dir, "keep1.md"); n == 0 {
		t.Error("keep1.md should survive the ignore reconcile")
	}
	// The pruned file is still on disk — this was an ignore-prune, not a deletion.
	if _, err := os.Stat(filepath.Join(dir, "drop.md")); err != nil {
		t.Errorf("drop.md should remain on disk: %v", err)
	}
}

// TestReconcileDeletedFiles_OverBroadIgnoreRefused verifies the blast-radius guard
// protects against a misconfigured ignore rule: a pattern that would drop >50% of
// the corpus is refused, leaving the index intact rather than emptying it.
func TestReconcileDeletedFiles_OverBroadIgnoreRefused(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const total = pruneBlastRadiusMinFiles + 2 // ≥ floor so the ratio gate is live
	for i := range total {
		indexPruneTestFile(t, dir, st, fmt.Sprintf("doc%02d.md", i), "# Doc\n\nmarker.\n")
	}
	// "*.md" matches every indexed file → 100% > 50% → must refuse.
	if err := os.WriteFile(filepath.Join(dir, ".docgraphignore"), []byte("*.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := scanner.NewIgnoreMatcher(dir, scanner.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if pruned := reconcileDeletedFiles(dir, st, m); pruned != 0 {
		t.Fatalf("expected over-broad ignore to be REFUSED (0 pruned), got %d", pruned)
	}
	if n := nodeCountSQL(t, dir, "doc00.md"); n == 0 {
		t.Error("index should be intact after a refused reconcile")
	}
}
