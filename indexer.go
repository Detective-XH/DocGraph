package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Detective-XH/docgraph/internal/codedoc"
	"github.com/Detective-XH/docgraph/internal/docformat"
	"github.com/Detective-XH/docgraph/internal/extractor"
	"github.com/Detective-XH/docgraph/internal/git"
	"github.com/Detective-XH/docgraph/internal/indexcore"
	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/resolver"
	"github.com/Detective-XH/docgraph/internal/scanner"
	"github.com/Detective-XH/docgraph/internal/similarity"
	"github.com/Detective-XH/docgraph/internal/store"
)

func indexPath(dir string) *store.Store {
	return indexPathOpts(dir, false)
}

// openStore opens (or creates) the store for dir without running indexStore.
// Used by cmdServe for async warm-start: open → register tools → listen → go indexStore.
func openStore(dir string) *store.Store {
	root, err := filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	dbDir := filepath.Join(root, ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		log.Fatal(err)
	}
	return st
}

// dbExists reports whether a docgraph.db already exists in dir's .docgraph directory.
func dbExists(dir string) bool {
	root, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(root, ".docgraph", "docgraph.db"))
	return err == nil
}

func indexPathOpts(dir string, force bool) *store.Store {
	root, err := filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	dbDir := filepath.Join(root, ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		log.Fatal(err)
	}

	// Snapshot pack enabled states before wiping the DB so user overrides survive.
	var savedPackEnabled map[string]bool
	if force {
		if existing, openErr := store.Open(filepath.Join(dbDir, "docgraph.db")); openErr == nil {
			if packs, pErr := existing.GetDomainPacks(); pErr == nil {
				savedPackEnabled = make(map[string]bool, len(packs))
				for _, p := range packs {
					savedPackEnabled[p.ID] = p.Enabled
				}
			}
			existing.Close()
		}
		if err := removeIndexDB(dbDir); err != nil {
			log.Fatal(err)
		}
	}

	st, err := store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		log.Fatal(err)
	}

	// Restore overridden pack states after force rebuild (before indexStore reads them).
	for id, enabled := range savedPackEnabled {
		if err := st.SetPackEnabled(id, enabled); err != nil {
			fmt.Fprintf(os.Stderr, "restore pack %s: %v\n", id, err)
		}
	}

	if err := indexStore(root, st, force); err != nil {
		log.Fatal(err)
	}
	return st
}

func removeIndexDB(dbDir string) error {
	for _, name := range []string{"docgraph.db", "docgraph.db-wal", "docgraph.db-shm"} {
		path := filepath.Join(dbDir, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

// indexStore scans root and writes nodes/edges/section chunks/FTS into st.
//
// force MUST mean "the DB was just wiped" (the --force path runs removeIndexDB
// before reopening, so base tables are empty). It is the gate for the per-file
// stale-row delete fast path: when force is true those deletes are skipped as
// no-ops. Passing force=true over a POPULATED DB would silently skip the
// load-bearing deletes and corrupt changed files (non-idempotent inserts). Do
// NOT key this off fullBuild (FTS-emptiness): that is also true after a
// crash-recovery over a populated base. Incremental callers (sync, serve
// warm-start, fsnotify watcher) pass force=false.
func indexStore(root string, st *store.Store, force bool) error {
	st.IndexMu.Lock()
	defer st.IndexMu.Unlock()

	entries, err := scanner.ScanDirOpts(root, scanner.ScanOptions{NoGitignore: noGitignore})
	if err != nil {
		return err
	}
	codeDocEnabled, _ := st.IsPackEnabled("code_doc")
	// Probe once: if root is not a git work tree (or git is absent), skip the
	// per-file CollectHistory fork entirely — on a non-repo tree every call is
	// a guaranteed fast-fail, thousands of wasted forks on a --force rebuild.
	// --no-history (noHistory) also opts out: git history is collected by default
	// (an LLM-first provenance/staleness signal, surfaced inline by docgraph_node),
	// but a user indexing a large git repo who doesn't want it skips the cost here.
	gitEnabled := git.IsRepo(root) && !noHistory

	fullBuild, err := indexStoreSetupFTS(st)
	if err != nil {
		return err
	}

	var nNew, nSkip int
	var changedDocIDs []string

	// batch-N: accumulate parsed files, then flush nodes + section chunks across
	// the batch in single InsertNodes/UpsertSectionChunks calls (see indexStoreFlush).
	// Those are the two FTS5-backed tables; coalescing collapses ~one commit-per-file
	// into ~one commit-per-batch. Profiling (full ~10.5k-file corpus) showed the win is
	// transaction/fsync overhead, NOT segment-merge: fts5IndexMergeLevel stays flat
	// (~9.5s) while sys time and commit count drop sharply — ~17% wall at N=500 vs
	// the per-file baseline.
	//
	// Dual bound: flush at batchN files OR batchBytes of accumulated section text,
	// whichever comes first. The byte cap keeps peak heap bounded on large-document
	// corpora where a pure file count could buffer far more than the ~90MB this
	// corpus adds. Flushing on a file boundary preserves crash-atomicity — a lost
	// batch re-indexes cleanly (file hashes are written in the flush via UpsertFile).
	// batch[:0] + batchTextBytes reset stay in this loop (the lifted indexStoreFlush
	// no longer owns the locals the former closure did).
	const batchN = 500
	const batchBytes = 32 << 20 // 32 MiB of section text
	batch := make([]*parser.ParseResult, 0, batchN)
	var batchTextBytes int

	for _, e := range entries {
		res, unchanged := indexStorePrepareEntry(st, e, codeDocEnabled, force)
		if unchanged {
			nSkip++
			continue
		}
		if res == nil {
			continue // excluded ext / read|delete|parse error (already logged)
		}
		batch = append(batch, res)
		for _, c := range res.SectionChunks {
			batchTextBytes += len(c.Text)
		}
		nNew++
		if len(batch) >= batchN || batchTextBytes >= batchBytes {
			if err := indexStoreFlush(st, root, gitEnabled, batch, &changedDocIDs); err != nil {
				return err
			}
			batch = batch[:0]
			batchTextBytes = 0
		}
	}
	if err := indexStoreFlush(st, root, gitEnabled, batch, &changedDocIDs); err != nil {
		return err
	}
	return indexStoreTail(st, fullBuild, len(entries), nNew, nSkip, changedDocIDs)
}

// indexStoreSetupFTS detects whether either FTS index is empty (a from-scratch or
// crash-recovery build) and, if so, drops the sync triggers so the per-file inserts
// bulk-load the base rows for a single optimal 'rebuild' pass in indexStoreTail.
//
// The AFTER INSERT triggers that tokenize text into the two FTS5 indexes
// (section_chunks_fts over section bodies, nodes_fts over name/qname/body_excerpt/
// metadata) are the #1 index cost (~42% of wall): incremental hash-flush+automerge
// per batch. On a from-scratch build (FTS empty) it is cheaper to drop the triggers,
// bulk-load the base rows, then 'rebuild' each FTS in one optimal pass (measured:
// section ~2.4x, nodes ~5x trigger cost on the real corpus). Incremental runs (FTS
// already populated) keep the triggers. The probe runs under IndexMu (held by the
// caller) so a watcher pass can't observe a transient empty FTS mid-build.
//
// Combined gate (sectionEmpty || nodesEmpty): the two rebuilds are independent, so a
// crash BETWEEN them leaves one FTS empty while the other is populated. ORing the two
// probes means the next run re-enters this path whenever EITHER index is empty; every
// file then hash-skips (nNew==0) and BOTH rebuilds re-run from the intact base tables.
// 'rebuild' is idempotent, so re-running the already-populated one is safe. This is why
// the tail rebuild is gated on fullBuild, NOT on nNew. The OR can only make the gate
// fire MORE often (toward the safe full-rebuild path), never less.
//
// Drop ALL three sync triggers per table. For section_chunks a fresh build still fires
// _update via UpsertSectionChunks' ON CONFLICT DO UPDATE on duplicate section node_ids;
// nodes uses INSERT OR IGNORE (no UPDATE) so only _insert would fire, but all three are
// dropped for symmetry and safety.
func indexStoreSetupFTS(st *store.Store) (bool, error) {
	sectionEmpty, ftsErr := st.Fts.SectionFTSIsEmpty()
	if ftsErr != nil {
		fmt.Fprintf(os.Stderr, "section FTS probe: %v\n", ftsErr)
		sectionEmpty = false // safe fallback: keep the trigger-driven path
	}
	nodesEmpty, nftsErr := st.Fts.NodesFTSIsEmpty()
	if nftsErr != nil {
		fmt.Fprintf(os.Stderr, "nodes FTS probe: %v\n", nftsErr)
		nodesEmpty = false // safe fallback: keep the trigger-driven path
	}
	fullBuild := sectionEmpty || nodesEmpty
	if fullBuild {
		if err := st.Fts.DropSectionFTSTriggers(); err != nil {
			return false, err
		}
		if err := st.Fts.DropNodesFTSTriggers(); err != nil {
			return false, err
		}
	}
	return fullBuild, nil
}

// indexStoreDeleteStale removes the stale section chunks, metadata, entity data, and
// node/edge/file rows for relPath before a re-parse, so cascade-deleted IDs are still
// reachable. The deletes are eager (phase 1): InsertEdges/InsertUnresolvedRefs are
// plain INSERTs, so stale rows must be gone before the batch flush re-inserts them.
// Returns the first delete error; the caller logs it and skips the file (matching the
// original per-delete log-then-continue, since only the first failure ran).
func indexStoreDeleteStale(st *store.Store, relPath string) error {
	if err := st.DeleteSectionChunksByFile(relPath); err != nil {
		return err
	}
	if err := st.DeleteDocumentMetadataByFile(relPath); err != nil {
		return err
	}
	if err := st.Entity.DeleteEntityData(relPath); err != nil {
		return err
	}
	if err := st.DeleteFileData(relPath); err != nil {
		return err
	}
	return nil
}

// indexStorePrepareEntry reads, hashes, deletes-stale (unless force), and parses one
// scanned entry. It returns (res, false) with a parsed result to batch, (nil, true)
// when the file is unchanged (hash hit → caller bumps nSkip), or (nil, false) to skip
// silently (excluded ext or a read/delete/parse error already logged to stderr — each
// was a `continue` in the original loop).
//
// force MUST mean "the DB was just wiped" (--force runs removeIndexDB before reopening,
// so base tables are empty): then the per-file deletes are skipped as no-ops. Guard on
// `force`, NOT on `fullBuild`: `fullBuild` is FTS-emptiness, which is ALSO true on a
// crash-recovery incremental run (base rows intact, FTS lost) — there a changed file's
// stale rows MUST still be deleted or the non-idempotent inserts duplicate edges /
// orphan chunks. force==true is the only signal meaning "DB empty ⟹ nothing to delete".
func indexStorePrepareEntry(st *store.Store, e scanner.FileEntry, codeDocEnabled, force bool) (*parser.ParseResult, bool) {
	ext := strings.ToLower(filepath.Ext(e.RelPath))
	if !codeDocEnabled && codedoc.IsCodeExt(ext) {
		return nil, false
	}
	src, err := docformat.ReadFileCapped(e.Path, docformat.MaxFileSizeByExt[ext])
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.RelPath, err)
		return nil, false
	}
	hash := sha256Hex(src)
	if old, _ := st.GetFileHash(e.RelPath); hash == old {
		return nil, true
	}
	if !force {
		if err := indexStoreDeleteStale(st, e.RelPath); err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", e.RelPath, err)
			return nil, false
		}
	}
	res, err := dispatchParse(e.Path, e.RelPath, src, hash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", e.RelPath, err)
		return nil, false
	}
	res.FileInfo.ModifiedAt = e.ModifiedAt
	return res, false
}

// indexStoreFlush writes one accumulated batch: nodes first (FK parents) then section
// chunks, both batched, followed by per-file dependent writes. It is the lifted form of
// the former flush closure; the caller resets batch[:0] + batchTextBytes after it
// returns. A nil/empty batch is a no-op.
func indexStoreFlush(st *store.Store, root string, gitEnabled bool, batch []*parser.ParseResult, changedDocIDs *[]string) error {
	if len(batch) == 0 {
		return nil
	}
	// Phase A — nodes first (FK parents), then section chunks, batched.
	var allNodes []store.Node
	var allChunks []store.SectionChunk
	for _, res := range batch {
		allNodes = append(allNodes, res.DocNode)
		allNodes = append(allNodes, res.Headings...)
		allNodes = append(allNodes, res.Defs...)
		allNodes = append(allNodes, res.Tags...)
		allChunks = append(allChunks, res.SectionChunks...)
	}
	if err := st.InsertNodes(allNodes); err != nil {
		return err
	}
	if len(allChunks) > 0 {
		if err := st.UpsertSectionChunks(allChunks); err != nil {
			return err
		}
	}
	// Phase B — per-file dependent writes (nodes now exist, so FKs resolve).
	// Collect git history for the whole batch up front, fanning the per-file
	// `git log --follow` forks across NumCPU workers. On a large versioned
	// corpus this is by far the dominant index cost (a serial loop pegs one
	// core on `--follow` rename detection while the rest idle); see
	// git.CollectHistories. The forks are pure and independent; the
	// UpsertFileHistory writes below stay serial under IndexMu (SQLite is a
	// single writer), so histories[idx] aligns with the batch order.
	var histories []git.FileHistory
	if gitEnabled {
		relPaths := make([]string, len(batch))
		for i, res := range batch {
			relPaths[i] = res.FileInfo.Path
		}
		histories = git.CollectHistories(root, relPaths, runtime.NumCPU())
	}
	for idx, res := range batch {
		var fh git.FileHistory
		if gitEnabled {
			fh = histories[idx]
		}
		if err := indexcore.WriteDependents(st, res, fh, gitEnabled, changedDocIDs, ""); err != nil {
			return err
		}
	}
	return nil
}

// indexStoreTail rebuilds both FTS indexes (only on a fullBuild) and restores the sync
// triggers, then prunes orphan entities and (when any file changed) runs the resolver +
// incremental similarity. Unlike indexProjectOpts, PruneOrphanEntities runs
// unconditionally here.
func indexStoreTail(st *store.Store, fullBuild bool, totalEntries, nNew, nSkip int, changedDocIDs []string) error {
	// Build both FTS indexes in one bulk pass each and restore the sync triggers.
	// Gated on fullBuild (not nNew): a crash-recovery run hash-skips every file
	// (nNew==0) yet still needs the FTS repopulated from the intact base tables.
	// Runs before resolver/similarity — those write only edges, never nodes/section.
	if fullBuild {
		// Order matters for crash-safety: recreate ALL triggers FIRST (both FTS still
		// empty), then run the rebuilds as the strict LAST writes. That way "FTS
		// non-empty" implies "triggers restored", so any crash lands on an empty FTS
		// and the combined fullBuild gate self-heals on the next run. 'rebuild' is an
		// FTS-only command (no INSERT into the base tables), so the live triggers
		// can't double-index during it. nodes_fts requires the renamed `metadata`
		// column (schema.go) for content reconstruction — see nodesFTSTriggersSQL.
		if err := st.Fts.CreateSectionFTSTriggers(); err != nil {
			return err
		}
		if err := st.Fts.CreateNodesFTSTriggers(); err != nil {
			return err
		}
		if err := st.Fts.RebuildSectionFTS(); err != nil {
			return err
		}
		if err := st.Fts.RebuildNodesFTS(); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "Indexed %d files (%d new, %d unchanged)\n", totalEntries, nNew, nSkip)
	if err := st.Entity.PruneOrphanEntities(); err != nil {
		fmt.Fprintf(os.Stderr, "prune orphan entities: %v\n", err)
	}
	if nNew > 0 {
		if err := resolver.Resolve(st); err != nil {
			fmt.Fprintf(os.Stderr, "resolver: %v\n", err)
		}
		if err := similarity.ComputeSimilarityIncremental(st, changedDocIDs, similarityThreshold); err != nil {
			fmt.Fprintf(os.Stderr, "similarity: %v\n", err)
		}
	}
	return nil
}

func dispatchParse(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ".md" {
		return parser.ParseFile(absPath, relPath, src, hash)
	}
	if codedoc.IsCodeExt(ext) {
		return codedoc.Extract(absPath, relPath, src, hash)
	}
	return extractor.Extract(absPath, relPath, src, hash)
}

func sha256Hex(d []byte) string {
	h := sha256.Sum256(d)
	return hex.EncodeToString(h[:])
}
