package indexcore

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/git"
	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/docgraph.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// docResult builds a minimal ParseResult whose dependent tail (no metadata, edges,
// or links) is a clean happy path: only UpsertFile + the changedDocIDs append do work.
func docResult(id, path string) *parser.ParseResult {
	return &parser.ParseResult{
		DocNode:  store.Node{ID: id, Kind: "document", Name: path, FilePath: path},
		FileInfo: store.FileInfo{Path: path, ContentHash: "hash-" + id},
	}
}

func TestWriteDependents_AppendsChangedDocIDAndPersistsFile(t *testing.T) {
	st := newStore(t)
	res := docResult("doc-1", "a.md")
	if err := st.InsertNodes([]store.Node{res.DocNode}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	var changed []string
	if err := WriteDependents(st, res, git.FileHistory{}, false, &changed, ""); err != nil {
		t.Fatalf("WriteDependents: %v", err)
	}

	if len(changed) != 1 || changed[0] != "doc-1" {
		t.Fatalf("changedDocIDs = %v, want [doc-1]", changed)
	}
	// UpsertFile must have run as part of the tail.
	if h, _ := st.GetFileHash("a.md"); h != "hash-doc-1" {
		t.Fatalf("GetFileHash = %q, want %q (UpsertFile did not persist)", h, "hash-doc-1")
	}
}

// TestWriteDependents_MetadataFailureFatalAndPrefixed locks two contract points at
// once: a metadata-write failure is FATAL (returned, not swallowed), and logPrefix is
// prepended verbatim — "" reproduces the CLI pipeline's unprefixed message, "[name] "
// reproduces the workspace pipeline's. Failure is forced by closing the store first.
func TestWriteDependents_MetadataFailureFatalAndPrefixed(t *testing.T) {
	tuple := []store.MetadataTuple{{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"}}

	for _, tc := range []struct {
		name      string
		logPrefix string
		wantHas   string
		wantNoHas string
	}{
		{"cli unprefixed", "", "metadata ", "["},
		{"workspace prefixed", "[proj] ", "[proj] metadata ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			st.Close() // force every DB write below to fail

			res := docResult("doc-1", "a.md")
			res.MetadataTuples = tuple

			var changed []string
			err := WriteDependents(st, res, git.FileHistory{}, false, &changed, tc.logPrefix)
			if err == nil {
				t.Fatal("WriteDependents returned nil, want fatal metadata error on closed store")
			}
			if !strings.Contains(err.Error(), tc.wantHas) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantHas)
			}
			if tc.wantNoHas != "" && strings.Contains(err.Error(), tc.wantNoHas) {
				t.Fatalf("error %q unexpectedly contains %q (logPrefix leaked)", err.Error(), tc.wantNoHas)
			}
		})
	}
}
