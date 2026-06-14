package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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

func (w *Workspace) IndexAll() error {
	// Index projects in parallel, bounded to NumCPU to avoid spawning hundreds of git subprocesses.
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for _, p := range w.Projects {
		p.NoGitignore = w.NoGitignore
		p.NoHistory = w.NoHistory
		p.SimilarityThreshold = w.SimilarityThreshold
		wg.Add(1)
		go func() {
			sem <- struct{}{}
			defer func() { <-sem; wg.Done() }()
			if err := indexProjectOpts(p, w.NoGitignore, w.SimilarityThreshold); err != nil {
				fmt.Fprintf(os.Stderr, "index %s: %v\n", p.Name, err)
			}
		}()
	}
	wg.Wait()

	// Second-pass: resolve cross-project [[project/doc-name]] wikilinks (requires all projects indexed).
	crossRefs := make([]resolver.ProjectRef, 0, len(w.Projects))
	for _, p := range w.Projects {
		crossRefs = append(crossRefs, resolver.ProjectRef{Name: p.Name, Store: p.Store})
	}
	if err := resolver.ResolveWorkspace(crossRefs); err != nil {
		fmt.Fprintf(os.Stderr, "[workspace] cross-project resolve: %v\n", err)
	}

	return nil
}

func ReindexProject(p *Project) {
	if err := indexProject(p); err != nil {
		fmt.Fprintf(os.Stderr, "[reindex] %s: %v\n", p.Name, err)
	}
}

func indexProject(p *Project) error {
	return indexProjectOpts(p, p.NoGitignore, p.SimilarityThreshold)
}

func indexProjectOpts(p *Project, noGitignore bool, threshold float64) error {
	p.Store.IndexMu.Lock()
	defer p.Store.IndexMu.Unlock()

	entries, err := scanner.ScanDirOpts(p.Path, scanner.ScanOptions{NoGitignore: noGitignore})
	if err != nil {
		return err
	}
	codeDocEnabled, err := p.Store.IsPackEnabled("code_doc")
	if err != nil {
		return fmt.Errorf("[%s] code_doc pack state: %w", p.Name, err)
	}
	// Probe once: if the project root is not a git work tree (or git is
	// absent), skip the per-file CollectHistory fork entirely — on a non-repo
	// tree every call is a guaranteed fast-fail, thousands of wasted forks.
	// --no-history (p.NoHistory, default off) also opts out of the collection.
	gitEnabled := git.IsRepo(p.Path) && !p.NoHistory
	// Cold-start fast path: on a fresh/empty project DB every file hash-misses and
	// the per-file deletes below match 0 rows. Skipping a 0-row DELETE is a true
	// no-op (no rows → no FK cascade → no AFTER DELETE FTS trigger fires; triggers
	// stay live on this path), so a cold-start skip is byte-identical to running
	// them. Probe once (under IndexMu) and skip when nodes is empty: that implies
	// the FK-cascade children (section_chunks, document_metadata, edges,
	// unresolved_refs) are empty, and `files` too — InsertNodes (in projectWriteOne)
	// precedes UpsertFile per file, so no file row outlives its nodes even under a
	// mid-loop crash. On an incremental run (populated DB) the deletes are
	// load-bearing — InsertEdges/InsertUnresolvedRefs are plain INSERTs — so they
	// must run for changed files. No force flag or FTS-rebuild gate here, so
	// base-table emptiness is the direct, safe signal (cf. indexStore's force guard).
	baseEmpty, emptyErr := p.Store.NodesIsEmpty()
	if emptyErr != nil {
		baseEmpty = false // safe fallback: keep the deletes
	}

	fullBuild, err := projectSetupFTS(p)
	if err != nil {
		return err
	}

	var nNew, nSkip int
	var changedDocIDs []string

	// gitEnabled=false — a non-repo child OR --no-history — gets NO benefit from
	// buffering, so it STREAMS each file straight through projectWriteOne, leaving
	// that path byte-identical to the pre-change code: the P1(d) non-repo fast path
	// the live serve --workspace tree relies on must stay unchanged, and gating on
	// gitEnabled (not git.IsRepo) is what also keeps --no-history on the stream path.
	// The git path buffers parsed results so the per-file `git log --follow` forks can
	// be collected at once (see projectFlushGitBatch).
	var batch []*parser.ParseResult
	if gitEnabled {
		batch = make([]*parser.ParseResult, 0, len(entries))
	}
	for _, e := range entries {
		res, unchanged := projectPrepareEntry(p, e, codeDocEnabled)
		if unchanged {
			nSkip++
			continue
		}
		if res == nil {
			continue // excluded ext / read|parse error (already logged)
		}
		if gitEnabled {
			// Defer the write: only changed (hash-miss + parse-success) files enter
			// the batch, so an incremental watcher reindex still forks git for just
			// the files it touched, never the whole project. The held batch costs
			// heap proportional to the changed-file set (only on the git path);
			// measured tiny and transient, so no windowing (cf. indexer.go's batchN).
			batch = append(batch, res)
			continue
		}
		if err := projectWriteOne(p, res, git.FileHistory{}, baseEmpty, gitEnabled, &changedDocIDs); err != nil {
			return err
		}
		nNew++
	}

	written, err := projectFlushGitBatch(p, batch, baseEmpty, gitEnabled, &changedDocIDs)
	nNew += written
	if err != nil {
		return err
	}

	return projectTail(p, fullBuild, len(entries), nNew, nSkip, changedDocIDs, threshold)
}

// projectSetupFTS detects whether either FTS index is empty (a from-scratch or
// crash-recovery build) and, if so, drops the sync triggers so the per-file inserts
// bulk-load the base rows for a single optimal 'rebuild' pass in projectTail.
// Mirrors indexStore's setup in indexer.go.
//
// The two AFTER INSERT triggers that tokenize text into section_chunks_fts (section
// bodies) and nodes_fts (name/qname/body_excerpt/metadata) are the dominant cold-start
// cost — a CPU profile put indexProjectOpts at ~52% with the delete block only ~4% of
// it, the rest trigger-driven FTS population. Incremental runs (FTS already populated →
// fullBuild=false) keep the triggers, so the watcher reindex (ReindexProject →
// indexProject) is byte-for-byte unchanged.
//
// Per-store + parallel-safe: IndexAll runs projects concurrently, each with its own
// p.Store (separate SQLite DB), so the trigger drop/recreate + probes here touch only
// this project's schema. The probes run under IndexMu (held by the caller) so the
// watcher can't observe a transient empty FTS mid-build.
//
// Combined gate (sectionEmpty || nodesEmpty) + crash self-heal: the two rebuilds are
// independent, so a crash between them leaves one FTS empty; ORing the probes re-enters
// this path whenever EITHER is empty and re-runs BOTH idempotent rebuilds from the
// intact base tables. This also makes the path correct in the crash-recovery state
// unique to this path — base rows present but FTS empty: there baseEmpty=false (the
// per-file deletes RUN, load-bearing for changed files) AND fullBuild=true (FTS rebuilt
// from the settled base afterward). Drop ALL three triggers per table: UpsertSectionChunks
// fires _update via its ON CONFLICT DO UPDATE on duplicate section node_ids; nodes uses
// INSERT OR IGNORE (only _insert), but all three drop for symmetry with section_chunks.
// nodes_fts 'rebuild' requires the renamed `metadata` column (schema.go) for content
// reconstruction (workspace DBs were migrated; fresh DBs bootstrap the current schema).
func projectSetupFTS(p *Project) (bool, error) {
	sectionEmpty, sErr := p.Store.Fts.SectionFTSIsEmpty()
	if sErr != nil {
		fmt.Fprintf(os.Stderr, "[%s] section FTS probe: %v\n", p.Name, sErr)
		sectionEmpty = false // safe fallback: keep the trigger-driven path
	}
	nodesEmpty, nErr := p.Store.Fts.NodesFTSIsEmpty()
	if nErr != nil {
		fmt.Fprintf(os.Stderr, "[%s] nodes FTS probe: %v\n", p.Name, nErr)
		nodesEmpty = false // safe fallback: keep the trigger-driven path
	}
	fullBuild := sectionEmpty || nodesEmpty
	if fullBuild {
		if err := p.Store.Fts.DropSectionFTSTriggers(); err != nil {
			return false, err
		}
		if err := p.Store.Fts.DropNodesFTSTriggers(); err != nil {
			return false, err
		}
	}
	return fullBuild, nil
}

// projectPrepareEntry reads, hashes, and parses one scanned entry. It returns
// (res, false) with a parsed result to index, (nil, true) when the file is unchanged
// (hash hit → caller bumps nSkip), or (nil, false) to skip silently (excluded ext or a
// read/parse error already logged to stderr — each was a `continue` in the original
// loop). It performs NO writes, so it never touches baseEmpty or the delete block.
func projectPrepareEntry(p *Project, e scanner.FileEntry, codeDocEnabled bool) (*parser.ParseResult, bool) {
	ext := strings.ToLower(filepath.Ext(e.RelPath))
	if !codeDocEnabled && codedoc.IsCodeExt(ext) {
		return nil, false
	}
	src, err := docformat.ReadFileCapped(e.Path, docformat.MaxFileSizeByExt[ext])
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.RelPath, err)
		return nil, false
	}
	h := sha256.Sum256(src)
	hash := hex.EncodeToString(h[:])
	if old, _ := p.Store.GetFileHash(e.RelPath); hash == old {
		return nil, true
	}
	res, err := parseIndexedFile(e.Path, e.RelPath, src, hash)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", e.RelPath, err)
		return nil, false
	}
	res.FileInfo.ModifiedAt = e.ModifiedAt
	return res, false
}

// projectWriteOne persists one parsed file. Delete-then-insert stays tight per file
// (same window as the pre-change path), and the git history — when enabled — is written
// INLINE here from the pre-collected fh, never a separate post-loop pass: a mid-loop
// error returns with every already-written file carrying its history row, so no
// committed file is left without history to hash-skip forever (the error-path-
// completeness regression that reverted the prior attempt). Shared by the streaming
// non-git path and the git write loop so both keep byte-identical per-file write
// semantics. The caller bumps nNew on success (writeOne incremented it inline before
// the refactor lifted this closure to a function).
func projectWriteOne(p *Project, res *parser.ParseResult, fh git.FileHistory, baseEmpty, gitEnabled bool, changedDocIDs *[]string) error {
	relPath := res.FileInfo.Path
	// Delete derived rows before DeleteFileData so cascade-deleted node IDs are
	// still reachable. Skipped on a cold-start (empty DB) where they are no-ops.
	if !baseEmpty {
		if err := p.Store.DeleteSectionChunksByFile(relPath); err != nil {
			return fmt.Errorf("delete %s: %w", relPath, err)
		}
		if err := p.Store.DeleteDocumentMetadataByFile(relPath); err != nil {
			return fmt.Errorf("delete %s: %w", relPath, err)
		}
		if err := p.Store.Entity.DeleteEntityData(relPath); err != nil {
			return fmt.Errorf("delete %s: %w", relPath, err)
		}
		if err := p.Store.DeleteFileData(relPath); err != nil {
			return fmt.Errorf("delete %s: %w", relPath, err)
		}
	}
	nodes := append([]store.Node{res.DocNode}, res.Headings...)
	nodes = append(nodes, res.Defs...)
	nodes = append(nodes, res.Tags...)
	if err := p.Store.InsertNodes(nodes); err != nil {
		return err
	}
	if len(res.SectionChunks) > 0 {
		if err := p.Store.UpsertSectionChunks(res.SectionChunks); err != nil {
			return err
		}
	}
	return indexcore.WriteDependents(p.Store, res, fh, gitEnabled, changedDocIDs, fmt.Sprintf("[%s] ", p.Name))
}

// projectFlushGitBatch writes the buffered git-path results, collecting every per-file
// `git log --follow` fork at once across NumCPU workers (globally bounded by
// git.forkSem so concurrent git children stay ≤ NumCPU even with NumCPU projects in
// flight). Rows return in batch order, so histories[idx] aligns with batch[idx]. It
// returns the number of files written so the caller can add it to nNew. A nil/empty
// batch (the streaming non-git path) is a no-op returning (0, nil).
func projectFlushGitBatch(p *Project, batch []*parser.ParseResult, baseEmpty, gitEnabled bool, changedDocIDs *[]string) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	relPaths := make([]string, len(batch))
	for i, res := range batch {
		relPaths[i] = res.FileInfo.Path
	}
	histories := git.CollectHistories(p.Path, relPaths, runtime.NumCPU())
	written := 0
	for idx, res := range batch {
		if err := projectWriteOne(p, res, histories[idx], baseEmpty, gitEnabled, changedDocIDs); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// projectTail rebuilds both FTS indexes (only on a fullBuild) and, when any file
// changed, prunes orphan entities and runs the resolver + incremental similarity.
func projectTail(p *Project, fullBuild bool, totalEntries, nNew, nSkip int, changedDocIDs []string, threshold float64) error {
	// Build both FTS indexes in one bulk pass each and restore the sync triggers.
	// Gated on fullBuild (not nNew): a crash-recovery run hash-skips every file
	// (nNew==0) yet still needs the FTS repopulated from the intact base tables.
	// Order matters for crash-safety: recreate ALL triggers FIRST (both FTS still
	// empty), then run the rebuilds as the strict LAST FTS writes — so "FTS
	// non-empty" implies "triggers restored" and any crash lands on an empty FTS
	// that the combined gate self-heals next run. Runs before resolver/similarity:
	// those write only edges (never nodes/section_chunks), so they neither need the
	// rebuilt FTS nor invalidate it, and the live triggers would keep it current
	// regardless. 'rebuild' is FTS-only (no base INSERT) so live triggers can't
	// double-index during it.
	if fullBuild {
		if err := p.Store.Fts.CreateSectionFTSTriggers(); err != nil {
			return err
		}
		if err := p.Store.Fts.CreateNodesFTSTriggers(); err != nil {
			return err
		}
		if err := p.Store.Fts.RebuildSectionFTS(); err != nil {
			return err
		}
		if err := p.Store.Fts.RebuildNodesFTS(); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "[%s] Indexed %d files (%d new, %d unchanged)\n", p.Name, totalEntries, nNew, nSkip)
	if nNew > 0 {
		// Redundant safety net mirroring indexStore's tail prune: DeleteEntityData
		// already prunes inline after every per-file delete (store/entity.go), so on
		// a changed-file run this rarely finds anything. Gated on nNew>0 (indexStore
		// calls it unconditionally) — sound because entities are only ever inserted
		// inside projectWriteOne, which the caller pairs with nNew++, so any run that
		// could create an orphan has nNew>0; a nNew==0 tick inserts and deletes
		// nothing and cannot leave one. The gate keeps the hot watcher no-op tick off
		// a full-table anti-join while preserving indexStore's belt-and-suspenders
		// semantics.
		if err := p.Store.Entity.PruneOrphanEntities(); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] prune orphan entities: %v\n", p.Name, err)
		}
		if err := resolver.Resolve(p.Store); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] resolver: %v\n", p.Name, err)
		}
		if err := similarity.ComputeSimilarityIncremental(p.Store, changedDocIDs, threshold); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] similarity: %v\n", p.Name, err)
		}
	}
	return nil
}

func parseIndexedFile(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ".md" {
		return parser.ParseFile(absPath, relPath, src, hash)
	}
	if codedoc.IsCodeExt(ext) {
		return codedoc.Extract(absPath, relPath, src, hash)
	}
	return extractor.Extract(absPath, relPath, src, hash)
}
