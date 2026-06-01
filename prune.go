package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/docformat"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

const (
	pruneBlastRadiusMax      = 0.5 // refuse if >50% of indexed files would be pruned
	pruneBlastRadiusMinFiles = 10  // skip the ratio gate below this many indexed files (tiny projects)
)

// reconcileDeletedFiles removes index data for files that vanished from disk while no
// watcher was running (deleted during serve downtime) — the gap the event-driven
// pruneDeletedFiles cannot see. Per-file os.Stat over every DB file row (no scan): a
// transient FS error is !IsNotExist so pruneDeletedFiles skips it; a gitignored-but-present
// file is found by Stat and kept — so this is policy-agnostic and immune to empty-scan
// mass-delete. Runs once at serve startup (warm path only). Returns count pruned.
func reconcileDeletedFiles(root string, st *store.Store) int {
	// Guard 1 — tree present (the ONLY way per-file Stat could mass-delete: whole tree gone).
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "[reconcile] skipped: project root %q not accessible\n", root)
		return 0
	}
	dbFiles, err := st.GetFiles("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[reconcile] skipped: GetFiles: %v\n", err)
		return 0 // never prune on a failed DB read
	}
	var absent []string
	for _, f := range dbFiles {
		if !docformat.SupportedExt(strings.ToLower(filepath.Ext(f.Path))) {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(root, f.Path)); os.IsNotExist(statErr) {
			absent = append(absent, f.Path) // present OR transient error → not absent
		}
	}
	if len(absent) == 0 {
		return 0
	}
	// Guard 2 — blast radius (ratio + absolute floor); refusal NAMES the remedy.
	// Denominator is the post-index corpus (reconcile runs after the startup index), so the
	// ratio is conservative-permissive — safe: per-file os.Stat is the real protection and
	// Guard 1 covers a gone/unmounted root; this only backstops a vanished sub-mount.
	if len(dbFiles) >= pruneBlastRadiusMinFiles &&
		float64(len(absent))/float64(len(dbFiles)) > pruneBlastRadiusMax {
		fmt.Fprintf(os.Stderr, "[reconcile] refused: would prune %d/%d files (>%.0f%%) — likely a moved/unmounted tree; run `docgraph index --force` if intentional\n",
			len(absent), len(dbFiles), pruneBlastRadiusMax*100)
		return 0
	}
	st.IndexMu.Lock()
	defer st.IndexMu.Unlock()
	// The re-stat inside pruneDeletedFiles is an intentional TOCTOU guard (delete+recreate
	// between our candidate-stat and its re-stat → seen present → not pruned), not redundant.
	return pruneDeletedFiles(root, st, absent)
}

// reconcileWorkspaceProjects runs the startup deletion-reconcile across every workspace
// project. PARITY: the single --path branch calls reconcileDeletedFiles directly; this is
// the --workspace counterpart — a future edit must not wire reconcile into one branch and
// miss the other (both live in cmd_serve.go doSync). Returns total nodes pruned.
func reconcileWorkspaceProjects(projects []*workspace.Project) int {
	total := 0
	for _, p := range projects {
		total += reconcileDeletedFiles(p.Path, p.Store)
	}
	return total
}

// pruneDeletedFiles removes index data for files the watcher flagged as changed
// that no longer exist on disk. The reindex (indexStore / indexProjectOpts) re-scans
// CURRENT files and delete-then-inserts each, but never prunes a file that VANISHED
// from disk, so its nodes/edges/FTS rows/metadata/entity mentions/file-hash row
// persist in the live index until `index --force` (AX probe v5 deletion-staleness).
// This is TARGETED: it only touches paths the watcher explicitly reported AND that
// os.Stat confirms are gone — so a .gitignore edit or a partial scan can never
// trigger a mass-delete (those produce no per-file Remove event).
// Returns the count pruned. `root` is the project root (absolute path),
// `changed` are project-relative paths from the watcher.
func pruneDeletedFiles(root string, st *store.Store, changed []string) int {
	pruned := 0
	for _, rel := range changed {
		if !docformat.SupportedExt(strings.ToLower(filepath.Ext(rel))) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			continue // still on disk (created/modified, or delete+recreate within debounce)
		} else if !os.IsNotExist(err) {
			continue // transient stat error — do NOT prune on uncertainty
		}
		if h, _ := st.GetFileHash(rel); h == "" {
			continue // not indexed (already pruned / never indexed) — nothing to do
		}
		// Mirror the reindex write path's per-file delete block so edge cascade
		// (FK ON DELETE CASCADE on edges.source+target), FTS sync (nodes_fts_delete +
		// section_chunks_fts_delete AFTER DELETE triggers), and entity-orphan prune
		// (DeleteEntityData prunes inline) all fire.
		st.DeleteSectionChunksByFile(rel)
		st.DeleteDocumentMetadataByFile(rel)
		st.Entity.DeleteEntityData(rel)
		if err := st.DeleteFileData(rel); err != nil {
			fmt.Fprintf(os.Stderr, "[prune] %s: %v\n", rel, err)
			continue
		}
		pruned++
	}
	if pruned > 0 {
		fmt.Fprintf(os.Stderr, "[prune] removed %d deleted file(s) from the index\n", pruned)
	}
	return pruned
}
