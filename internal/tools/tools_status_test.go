package tools

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

// seedStatusStore opens a fresh store holding one frontmatter-less document node
// (so it counts as enrichment-eligible) plus entityCount entities and matching
// mentions, giving the workspace status fan-out something to aggregate.
func seedStatusStore(t *testing.T, docID string, entityCount int) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.InsertNodes([]store.Node{{
		ID: docID, Kind: "document", Name: docID, QualifiedName: docID,
		FilePath: docID, StartLine: 1, EndLine: 1, UpdatedAt: 1,
	}}); err != nil {
		t.Fatalf("InsertNodes(%s): %v", docID, err)
	}

	var ents []store.Entity
	var mentions []store.Mention
	for i := range entityCount {
		id := fmt.Sprintf("%s-ent-%d", docID, i)
		ents = append(ents, store.Entity{
			ID: id, EntityType: "person",
			CanonicalName: id, CanonicalNameNormalized: id,
		})
		mentions = append(mentions, store.Mention{
			EntityID: id, NodeID: docID, FilePath: docID, Line: i + 1,
		})
	}
	if err := st.Entity.InsertEntities(ents); err != nil {
		t.Fatalf("InsertEntities(%s): %v", docID, err)
	}
	if err := st.Entity.InsertEntityMentions(mentions); err != nil {
		t.Fatalf("InsertEntityMentions(%s): %v", docID, err)
	}
	return st
}

// TestHandleStatusWorkspaceFanOut exercises the workspace-mode status branch
// end-to-end, verifying it aggregates entity, enrichment, and drift stats across
// projects. This covers the fan-out helpers added for workspace/single-store
// status parity: the wired-in workspace.GetEntityStats (previously dead),
// appendWorkspaceEnrichmentStats, and appendWorkspaceDriftAudit.
func TestHandleStatusWorkspaceFanOut(t *testing.T) {
	storeA := seedStatusStore(t, "a.md", 2)
	storeB := seedStatusStore(t, "b.md", 1)

	// Make b.md trigger exactly one policy.stale_review drift finding:
	// approved doc whose review_due is far in the past.
	if err := storeB.UpsertGovernanceMetadata("b.md", []store.MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "review_due", Value: "2020-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	h := &handler{workspace: &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "project-a", Path: t.TempDir(), Store: storeA},
		{Name: "project-b", Path: t.TempDir(), Store: storeB},
	}}}

	res, err := callTool(h, h.handleStatus, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	// Each section below comes from a distinct workspace fan-out path.
	wants := []string{
		"## DocGraph Workspace Status",           // workspace branch executed
		"### Entity Graph",                       // workspace.GetEntityStats wired in
		"Entities: 3 | Mentions: 3",              // 2 in project-a + 1 in project-b, summed
		"### Agent Metadata Enrichment",          // appendWorkspaceEnrichmentStats
		"Eligible frontmatter-less documents: 2", // a.md + b.md, summed across projects
		"### Drift Audit",                        // appendWorkspaceDriftAudit
		"Total findings: 1",                      // single stale_review finding in project-b
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("workspace status output missing %q\n--- full output ---\n%s", want, text)
		}
	}
}
