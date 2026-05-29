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
					savedPackEnabled[p.ID] = p.EnabledByDefault
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
	var nNew, nSkip int
	var changedDocIDs []string
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
		st.DeleteSectionChunksByFile(e.RelPath)
		st.DeleteDocumentMetadataByFile(e.RelPath)
		st.DeleteEntityData(e.RelPath)
		st.DeleteFileData(e.RelPath)
		res, err := dispatchParse(e.Path, e.RelPath, src, hash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", e.RelPath, err)
			continue
		}
		nodes := append([]store.Node{res.DocNode}, res.Headings...)
		nodes = append(nodes, res.Defs...)
		nodes = append(nodes, res.Tags...)
		res.FileInfo.ModifiedAt = e.ModifiedAt
		if err := st.InsertNodes(nodes); err != nil {
			return err
		}
		if len(res.SectionChunks) > 0 {
			if err := st.UpsertSectionChunks(res.SectionChunks); err != nil {
				return err
			}
		}
		if len(res.MetadataTuples) > 0 {
			if err := st.InsertDocumentMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
				return fmt.Errorf("metadata %s: %w", e.RelPath, err)
			} else if err := st.UpsertGovernanceMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
				return fmt.Errorf("governance %s: %w", e.RelPath, err)
			} else if err := st.UpsertResearchMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
				return fmt.Errorf("research %s: %w", e.RelPath, err)
			}
		}
		if err := entitygraph.IndexFile(st, e.RelPath, res); err != nil {
			fmt.Fprintf(os.Stderr, "entity index %s: %v\n", e.RelPath, err)
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
					FilePath:      e.RelPath,
				})
			}
			if err := st.InsertUnresolvedRefs(refs); err != nil {
				return err
			}
		}
		if err := st.UpsertFile(res.FileInfo); err != nil {
			return err
		}
		h := git.CollectHistory(root, e.RelPath)
		if err := st.UpsertFileHistory(store.FileHistory{
			Path:          h.Path,
			CommitCount:   h.CommitCount,
			FirstCommitAt: h.FirstCommitAt,
			LastCommitAt:  h.LastCommitAt,
			AuthorCount:   h.AuthorCount,
			LastAuthor:    h.LastAuthor,
			LastSubject:   h.LastSubject,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "history %s: %v\n", e.RelPath, err)
		}
		nNew++
		changedDocIDs = append(changedDocIDs, res.DocNode.ID)
	}
	fmt.Fprintf(os.Stderr, "Indexed %d files (%d new, %d unchanged)\n", len(entries), nNew, nSkip)
	if err := st.PruneOrphanEntities(); err != nil {
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
