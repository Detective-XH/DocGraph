package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

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

func indexPathOpts(dir string, force bool) *store.Store {
	root, err := filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	dbDir := filepath.Join(root, ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if force {
		if err := removeIndexDB(dbDir); err != nil {
			log.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		log.Fatal(err)
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
	entries, err := scanner.ScanDirOpts(root, scanner.ScanOptions{NoGitignore: noGitignore})
	if err != nil {
		return err
	}
	var nNew, nSkip int
	var changedDocIDs []string
	for _, e := range entries {
		src, err := os.ReadFile(e.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.RelPath, err)
			continue
		}
		hash := sha256Hex(src)
		if old, _ := st.GetFileHash(e.RelPath); hash == old {
			nSkip++
			continue
		}
		// Delete stale section chunks and document metadata before DeleteFileData
		// so cascade-deleted node IDs are still reachable. All calls are idempotent.
		st.DeleteSectionChunksByFile(e.RelPath)
		st.DeleteDocumentMetadataByFile(e.RelPath)
		st.DeleteFileData(e.RelPath)
		res, err := parser.ParseFile(e.Path, e.RelPath, src, hash)
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
	if nNew > 0 {
		if err := resolver.Resolve(st); err != nil {
			fmt.Fprintf(os.Stderr, "resolver: %v\n", err)
		}
		if err := similarity.ComputeSimilarityIncremental(st, changedDocIDs, similarityThreshold); err != nil {
			fmt.Fprintf(os.Stderr, "similarity: %v\n", err)
		}

		// Clear reindex_required marker only when every file was fully reparsed
		// (nSkip == 0), meaning section_chunks and document_metadata are complete.
		// If any files were skipped, the marker stays so the user knows a
		// --force reindex is still needed.
		if nSkip == 0 {
			scope, _, _ := st.GetProjectMeta(store.MetaKeyReindexScope)
			if scope == "sections" || scope == "metadata" {
				if err := st.DeleteProjectMeta(
					store.MetaKeyReindexRequired,
					store.MetaKeyReindexScope,
					store.MetaKeyReindexReason,
				); err != nil {
					fmt.Fprintf(os.Stderr, "clear reindex marker: %v\n", err)
				}
			}
		}
	}
	return nil
}

func sha256Hex(d []byte) string {
	h := sha256.Sum256(d)
	return hex.EncodeToString(h[:])
}
