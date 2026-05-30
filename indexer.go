package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/codedoc"
	"github.com/Detective-XH/docgraph/internal/docformat"
	"github.com/Detective-XH/docgraph/internal/entitygraph"
	"github.com/Detective-XH/docgraph/internal/extractor"
	"github.com/Detective-XH/docgraph/internal/git"
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

	if err := indexStore(root, st); err != nil {
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

func indexStore(root string, st *store.Store) error {
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
	gitEnabled := git.IsRepo(root)

	// FTS bulk-rebuild fast path. The AFTER INSERT triggers that tokenize text into
	// the two FTS5 indexes (section_chunks_fts over section bodies, nodes_fts over
	// name/qname/body_excerpt/metadata) are the #1 index cost (~42% of wall):
	// incremental hash-flush+automerge per batch. On a from-scratch build (FTS empty)
	// it is cheaper to drop the triggers, bulk-load the base rows, then 'rebuild' each
	// FTS in one optimal pass (measured: section ~2.4x, nodes ~5x trigger cost on the
	// real corpus). Incremental runs (FTS already populated) keep the triggers. The
	// probe runs under IndexMu (held above) so a watcher pass can't observe a
	// transient empty FTS mid-build.
	//
	// Combined gate (sectionEmpty || nodesEmpty): the two rebuilds are independent, so
	// a crash BETWEEN them leaves one FTS empty while the other is populated. ORing the
	// two probes means the next run re-enters this path whenever EITHER index is empty;
	// every file then hash-skips (nNew==0) and BOTH rebuilds re-run from the intact base
	// tables. 'rebuild' is idempotent, so re-running the already-populated one is safe.
	// This is why the tail rebuild is gated on fullBuild, NOT on nNew. The OR can only
	// make the gate fire MORE often (toward the safe full-rebuild path), never less.
	//
	// Drop ALL three sync triggers per table. For section_chunks a fresh build still
	// fires _update via UpsertSectionChunks' ON CONFLICT DO UPDATE on duplicate section
	// node_ids; nodes uses INSERT OR IGNORE (no UPDATE) so only _insert would fire, but
	// all three are dropped for symmetry and safety.
	sectionEmpty, ftsErr := st.SectionFTSIsEmpty()
	if ftsErr != nil {
		fmt.Fprintf(os.Stderr, "section FTS probe: %v\n", ftsErr)
		sectionEmpty = false // safe fallback: keep the trigger-driven path
	}
	nodesEmpty, nftsErr := st.NodesFTSIsEmpty()
	if nftsErr != nil {
		fmt.Fprintf(os.Stderr, "nodes FTS probe: %v\n", nftsErr)
		nodesEmpty = false // safe fallback: keep the trigger-driven path
	}
	fullBuild := sectionEmpty || nodesEmpty
	if fullBuild {
		if err := st.DropSectionFTSTriggers(); err != nil {
			return err
		}
		if err := st.DropNodesFTSTriggers(); err != nil {
			return err
		}
	}

	var nNew, nSkip int
	var changedDocIDs []string

	// batch-N: accumulate parsed files, then flush nodes + section chunks across
	// the batch in single InsertNodes/UpsertSectionChunks calls. Those are the two
	// FTS5-backed tables; coalescing collapses ~one commit-per-file into ~one
	// commit-per-batch. Profiling (full Code-Space, 10.5k files) showed the win is
	// transaction/fsync overhead, NOT segment-merge: fts5IndexMergeLevel stays flat
	// (~9.5s) while sys time and commit count drop sharply — ~17% wall at N=500 vs
	// the per-file baseline. The non-FTS dependent writes stay a per-file loop inside
	// the flush — they only need to run AFTER the batch node insert so FK references
	// (section_chunks/edges/entity_mentions → nodes) resolve.
	//
	// Dual bound: flush at batchN files OR batchBytes of accumulated section text,
	// whichever comes first. The byte cap keeps peak heap bounded on large-document
	// corpora where a pure file count could buffer far more than the ~90MB this
	// corpus adds. Flushing on a file boundary preserves crash-atomicity — a lost
	// batch re-indexes cleanly (file hashes are written in the flush via UpsertFile).
	const batchN = 500
	const batchBytes = 32 << 20 // 32 MiB of section text
	batch := make([]*parser.ParseResult, 0, batchN)
	var batchTextBytes int

	flush := func() error {
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
		for _, res := range batch {
			relPath := res.FileInfo.Path
			if len(res.MetadataTuples) > 0 {
				if err := st.InsertDocumentMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
					return fmt.Errorf("metadata %s: %w", relPath, err)
				} else if err := st.UpsertGovernanceMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
					return fmt.Errorf("governance %s: %w", relPath, err)
				} else if err := st.UpsertResearchMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
					return fmt.Errorf("research %s: %w", relPath, err)
				}
			}
			if err := entitygraph.IndexFile(st, relPath, res); err != nil {
				fmt.Fprintf(os.Stderr, "entity index %s: %v\n", relPath, err)
			}
			if err := st.InsertEdges(res.Edges); err != nil {
				return err
			}
			if len(res.RawLinks) > 0 {
				refs := make([]store.UnresolvedRef, 0, len(res.RawLinks))
				for _, rl := range res.RawLinks {
					refs = append(refs, store.UnresolvedRef{
						FromNodeID:    rl.FromNodeID,
						ReferenceText: rl.Target,
						ReferenceKind: rl.Kind,
						Line:          rl.Line,
						Col:           0,
						FilePath:      relPath,
					})
				}
				if err := st.InsertUnresolvedRefs(refs); err != nil {
					return err
				}
			}
			if err := st.UpsertFile(res.FileInfo); err != nil {
				return err
			}
			if gitEnabled {
				h := git.CollectHistory(root, relPath)
				if err := st.UpsertFileHistory(store.FileHistory{
					Path:          h.Path,
					CommitCount:   h.CommitCount,
					FirstCommitAt: h.FirstCommitAt,
					LastCommitAt:  h.LastCommitAt,
					AuthorCount:   h.AuthorCount,
					LastAuthor:    h.LastAuthor,
					LastSubject:   h.LastSubject,
				}); err != nil {
					fmt.Fprintf(os.Stderr, "history %s: %v\n", relPath, err)
				}
			}
			changedDocIDs = append(changedDocIDs, res.DocNode.ID)
		}
		batch = batch[:0]
		batchTextBytes = 0
		return nil
	}

	for _, e := range entries {
		if !codeDocEnabled && codedoc.IsCodeExt(strings.ToLower(filepath.Ext(e.RelPath))) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.RelPath))
		src, err := docformat.ReadFileCapped(e.Path, docformat.MaxFileSizeByExt[ext])
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.RelPath, err)
			continue
		}
		hash := sha256Hex(src)
		if old, _ := st.GetFileHash(e.RelPath); hash == old {
			nSkip++
			continue
		}
		// Delete stale section chunks, metadata, entity data, and node/edge data
		// before re-parsing so cascade-deleted IDs are still reachable. Idempotent.
		// Eager (phase 1): InsertEdges/InsertUnresolvedRefs are plain INSERTs, so the
		// stale rows must be gone before the batch flush re-inserts them.
		st.DeleteSectionChunksByFile(e.RelPath)
		st.DeleteDocumentMetadataByFile(e.RelPath)
		st.Entity.DeleteEntityData(e.RelPath)
		st.DeleteFileData(e.RelPath)
		res, err := dispatchParse(e.Path, e.RelPath, src, hash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", e.RelPath, err)
			continue
		}
		res.FileInfo.ModifiedAt = e.ModifiedAt
		batch = append(batch, res)
		for _, c := range res.SectionChunks {
			batchTextBytes += len(c.Text)
		}
		nNew++
		if len(batch) >= batchN || batchTextBytes >= batchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
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
		if err := st.CreateSectionFTSTriggers(); err != nil {
			return err
		}
		if err := st.CreateNodesFTSTriggers(); err != nil {
			return err
		}
		if err := st.RebuildSectionFTS(); err != nil {
			return err
		}
		if err := st.RebuildNodesFTS(); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "Indexed %d files (%d new, %d unchanged)\n", len(entries), nNew, nSkip)
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
