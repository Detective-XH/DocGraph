// Package indexcore holds the per-file indexing logic shared by DocGraph's two
// indexing pipelines: indexStore (indexer.go, the CLI index/sync + serve --path
// path) and indexProjectOpts (internal/workspace/indexing.go, the serve
// --workspace + watcher path). It lives in its own package because the shared code
// needs both parser.ParseResult and store.Store, and parser already imports store
// (so the helper cannot live in store without a cycle) while its two callers sit in
// different packages (package main and package workspace).
//
// Scope is deliberately narrow: only the byte-identical *dependent tail* — the
// per-file writes that must run AFTER a file's nodes + section chunks already exist
// (so foreign keys resolve). The divergent orchestration stays caller-side: node /
// section-chunk inserts (batched in indexStore, per-file in the workspace path), the
// stale-row delete block (guarded by force vs baseEmpty, and placed differently),
// the FTS bulk-rebuild trigger gate, and the nNew counter. See
// plans/index-pipeline-parity.md for why the pipelines are mirrored, not merged.
package indexcore

import (
	"fmt"
	"os"

	"github.com/Detective-XH/docgraph/internal/entitygraph"
	"github.com/Detective-XH/docgraph/internal/git"
	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// DependentWriter is the narrow persistence surface WriteDependents needs. It
// lists every store method WriteDependents calls directly, PLUS the two
// entity-graph writes that entitygraph.IndexFile needs: because WriteDependents
// forwards st to entitygraph.IndexFile (which takes an EntityWriter),
// DependentWriter's method set must be a superset of entitygraph.EntityWriter,
// so the two entity methods are required and must match it exactly. *store.Store
// satisfies the whole set (the entity methods via internal/store/forwarders.go).
type DependentWriter interface {
	InsertDocumentMetadata(nodeID string, tuples []store.MetadataTuple) error
	UpsertGovernanceMetadata(nodeID string, tuples []store.MetadataTuple) error
	UpsertResearchMetadata(nodeID string, tuples []store.MetadataTuple) error
	InsertEdges(edges []store.Edge) error
	InsertUnresolvedRefs(refs []store.UnresolvedRef) error
	UpsertFile(f store.FileInfo) error
	UpsertFileHistory(h store.FileHistory) error

	// Required so st can be passed to entitygraph.IndexFile(EntityWriter);
	// keep these identical to entitygraph.EntityWriter's method set.
	InsertEntities([]store.Entity) error
	InsertEntityMentions([]store.Mention) error
}

var _ DependentWriter = (*store.Store)(nil)

// WriteDependents persists the per-file dependent rows that must be written after a
// file's nodes and section chunks already exist: document / governance / research
// metadata, the entity graph, edges, unresolved references, the file row, and (when
// gitEnabled) the git history — then records the document ID in changedDocIDs for the
// downstream resolver + similarity pass.
//
// It is the verbatim tail shared by both pipelines (indexer.go's batched Phase-B loop
// and workspace indexing.go's per-file writeOne). The ONLY difference between the two
// original call sites was the error / stderr message prefix, threaded here as
// logPrefix: "" for the CLI / serve --path pipeline and "[<project>] " for the
// workspace pipeline. Both call sites' messages are reproduced byte-for-byte.
//
// It does NOT insert nodes or section chunks, run the stale-row delete block, or own
// the nNew counter — those stay caller-side because they differ between the pipelines
// (batched vs per-file, force vs baseEmpty). Metadata, edge, file, and unresolved-ref
// failures are fatal (returned); entity-graph and git-history failures are non-fatal
// (logged to stderr) so a bad entity pass or history fork never aborts indexing a
// document's nodes/edges — matching the pre-extraction behavior of both pipelines.
func WriteDependents(
	st DependentWriter,
	res *parser.ParseResult,
	fh git.FileHistory,
	gitEnabled bool,
	changedDocIDs *[]string,
	logPrefix string,
) error {
	relPath := res.FileInfo.Path
	if len(res.MetadataTuples) > 0 {
		if err := st.InsertDocumentMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
			return fmt.Errorf("%smetadata %s: %w", logPrefix, relPath, err)
		} else if err := st.UpsertGovernanceMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
			return fmt.Errorf("%sgovernance %s: %w", logPrefix, relPath, err)
		} else if err := st.UpsertResearchMetadata(res.DocNode.ID, res.MetadataTuples); err != nil {
			return fmt.Errorf("%sresearch %s: %w", logPrefix, relPath, err)
		}
	}
	// Entity graph: non-fatal. A failed entity pass must not abort indexing the
	// document's nodes/edges (the drift that once left serve --workspace with zero
	// entities was a *missing* call, not a fatal one — see parity doc History).
	if err := entitygraph.IndexFile(st, relPath, res); err != nil {
		fmt.Fprintf(os.Stderr, "%sentity index %s: %v\n", logPrefix, relPath, err)
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
		if err := st.UpsertFileHistory(store.FileHistory{
			Path:          fh.Path,
			CommitCount:   fh.CommitCount,
			FirstCommitAt: fh.FirstCommitAt,
			LastCommitAt:  fh.LastCommitAt,
			AuthorCount:   fh.AuthorCount,
			LastAuthor:    fh.LastAuthor,
			LastSubject:   fh.LastSubject,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "%shistory %s: %v\n", logPrefix, relPath, err)
		}
	}
	*changedDocIDs = append(*changedDocIDs, res.DocNode.ID)
	return nil
}
