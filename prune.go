package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/docformat"
	"github.com/Detective-XH/docgraph/internal/scanner"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

const (
	pruneBlastRadiusMax      = 0.5 // refuse if >50% of indexed files would be pruned
	pruneBlastRadiusMinFiles = 10  // skip the ratio gate below this many indexed files (tiny projects)
)

// FileDeleter is the narrow store surface the prune/reconcile path depends on:
// the index lock (so a prune serialises against reindex) plus the per-file
// read/delete primitives that remove one file's index data. Declaring exactly
// this surface lets the deletion functions accept any store-shaped value while
// documenting their real dependency; *store.Store satisfies it via its embedded
// IndexMu (LockIndex/UnlockIndex) and entity-substore forwarder (DeleteEntityData).
type FileDeleter interface {
	LockIndex()
	UnlockIndex()
	GetFileHash(string) (string, error)
	GetFiles(string) ([]store.FileInfo, error)
	DeleteSectionChunksByFile(string) error
	DeleteDocumentMetadataByFile(string) error
	DeleteEntityData(string) error
	DeleteFileData(string) error
}

var _ FileDeleter = (*store.Store)(nil)

// deleteIndexedFile removes all index data for one project-relative path: section
// chunks, document metadata, entity mentions, then the node/edge/file rows. The
// order mirrors the reindex write path so the edge cascade (FK ON DELETE CASCADE on
// edges.source+target), FTS sync (nodes_fts_delete + section_chunks_fts_delete AFTER
// DELETE triggers), and entity-orphan prune (DeleteEntityData prunes inline) all
// fire. Returns false (without error to the caller) when the path is not indexed or
// a delete step fails — the caller treats that as "nothing pruned". Callers decide
// WHETHER a path should be pruned (absent from disk, or ignore-matched); this only
// performs the deletion. Run with the index lock held (see LockIndex).
func deleteIndexedFile(st FileDeleter, rel string) bool {
	if h, _ := st.GetFileHash(rel); h == "" {
		return false // not indexed (already pruned / never indexed) — nothing to do
	}
	if err := st.DeleteSectionChunksByFile(rel); err != nil {
		fmt.Fprintf(os.Stderr, "[prune] %s: %v\n", rel, err)
		return false
	}
	if err := st.DeleteDocumentMetadataByFile(rel); err != nil {
		fmt.Fprintf(os.Stderr, "[prune] %s: %v\n", rel, err)
		return false
	}
	if err := st.DeleteEntityData(rel); err != nil {
		fmt.Fprintf(os.Stderr, "[prune] %s: %v\n", rel, err)
		return false
	}
	if err := st.DeleteFileData(rel); err != nil {
		fmt.Fprintf(os.Stderr, "[prune] %s: %v\n", rel, err)
		return false
	}
	return true
}

// classifyOutOfScope scans dbFiles and splits them into files absent from disk
// and files present but covered by an ignore rule. Transient stat errors are
// treated as "keep" (never classified). ignoreMatch may be nil.
func classifyOutOfScope(root string, dbFiles []store.FileInfo, ignoreMatch func(string) bool) (absent, ignored []string) {
	for _, f := range dbFiles {
		if !docformat.SupportedExt(strings.ToLower(filepath.Ext(f.Path))) {
			continue
		}
		_, statErr := os.Stat(filepath.Join(root, f.Path))
		if os.IsNotExist(statErr) {
			absent = append(absent, f.Path)
			continue
		}
		if statErr != nil {
			continue // transient error → keep (never prune on uncertainty)
		}
		// Present on disk: prune only if a deliberate ignore rule now covers it.
		if ignoreMatch != nil && ignoreMatch(f.Path) {
			ignored = append(ignored, f.Path)
		}
	}
	return absent, ignored
}

// reconcileDeletedFiles removes index data for files that are no longer in scope:
// (1) deleted from disk while no watcher was running (the gap the event-driven
// pruneDeletedFiles cannot see), found by per-file os.Stat; and (2) — when
// ignoreMatch is non-nil — files still on disk but now covered by an active ignore
// rule (e.g. a `.docgraphignore` pattern an agent just added). The reindex never
// removes either, because it only re-scans files that ARE in scope.
//
// Both signals are precise and safe against mass-delete: a transient FS error is
// !IsNotExist so the file is kept, and ignoreMatch tests the same ignore rules the
// scan applies (a present, ignore-matched file is unambiguously meant to be
// excluded) — neither is the empty-scan / partial-walk that raw scan-set membership
// would conflate. A blast-radius guard backstops both. Returns count pruned.
func reconcileDeletedFiles(root string, st FileDeleter, ignoreMatch func(string) bool) int {
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
	absent, ignored := classifyOutOfScope(root, dbFiles, ignoreMatch)
	toPrune := len(absent) + len(ignored)
	if toPrune == 0 {
		return 0
	}
	// Guard 2 — blast radius (ratio + absolute floor) over the COMBINED set; refusal
	// NAMES the remedy. Covers both a vanished sub-mount and an over-broad ignore rule
	// (e.g. a `.docgraphignore` of `*` or `/`): rather than silently emptying the index,
	// refuse and tell the user to rebuild explicitly.
	if len(dbFiles) >= pruneBlastRadiusMinFiles &&
		float64(toPrune)/float64(len(dbFiles)) > pruneBlastRadiusMax {
		fmt.Fprintf(os.Stderr, "[reconcile] refused: would prune %d/%d files (>%.0f%%) — likely a moved/unmounted tree or an over-broad ignore rule; run `docgraph index --force` if intentional\n",
			toPrune, len(dbFiles), pruneBlastRadiusMax*100)
		return 0
	}
	st.LockIndex()
	defer st.UnlockIndex()
	pruned := 0
	for _, rel := range absent {
		// TOCTOU guard: a delete+recreate between the candidate stat above and now
		// means the file is back — do not prune it.
		if _, statErr := os.Stat(filepath.Join(root, rel)); statErr == nil {
			continue
		}
		if deleteIndexedFile(st, rel) {
			pruned++
		}
	}
	for _, rel := range ignored {
		// Present by design (ignore-matched); prune directly.
		if deleteIndexedFile(st, rel) {
			pruned++
		}
	}
	if pruned > 0 {
		fmt.Fprintf(os.Stderr, "[reconcile] removed %d out-of-scope file(s) from the index\n", pruned)
	}
	return pruned
}

// reconcileWorkspaceProjects runs the startup deletion+ignore reconcile across every
// workspace project, building a per-project ignore matcher from the workspace's
// NoGitignore setting. PARITY: the single --path branch calls reconcileDeletedFiles
// directly; this is the --workspace counterpart — a future edit must not wire
// reconcile into one branch and miss the other (both live in cmd_serve.go doSync).
// Returns total files pruned.
func reconcileWorkspaceProjects(projects []*workspace.Project, noGitignore bool) int {
	total := 0
	for _, p := range projects {
		m, err := scanner.NewIgnoreMatcher(p.Path, scanner.ScanOptions{NoGitignore: noGitignore})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[reconcile] %s: ignore matcher: %v\n", p.Name, err)
			m = nil // degrade to deletion-only reconcile
		}
		total += reconcileDeletedFiles(p.Path, p.Store, m)
	}
	return total
}

// containsIgnoreRuleFile reports whether any watcher-reported path is an ignore-rule
// file (.docgraphignore / .gitignore), signalling that the in-scope file set may
// have changed and an ignore-aware reconcileDeletedFiles pass should run.
func containsIgnoreRuleFile(changed []string) bool {
	for _, rel := range changed {
		switch filepath.Base(rel) {
		case ".docgraphignore", ".gitignore":
			return true
		}
	}
	return false
}

// pruneDeletedFiles removes index data for files the watcher flagged as changed
// that no longer exist on disk — the targeted, event-driven counterpart to the
// startup reconcile. It only touches paths the watcher explicitly reported AND that
// os.Stat confirms are gone, so a .gitignore/.docgraphignore edit or a partial scan
// can never trigger a mass-delete here (those produce no per-file Remove event; the
// ignore-driven prune lives in reconcileDeletedFiles, guarded). Returns count pruned.
// `root` is the absolute project root, `changed` are project-relative watcher paths.
func pruneDeletedFiles(root string, st FileDeleter, changed []string) int {
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
		if deleteIndexedFile(st, rel) {
			pruned++
		}
	}
	if pruned > 0 {
		fmt.Fprintf(os.Stderr, "[prune] removed %d deleted file(s) from the index\n", pruned)
	}
	return pruned
}
