package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/docformat"
	"github.com/Detective-XH/docgraph/internal/store"
)

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
