package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexAllWritesSectionChunksAndMetadata(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `---
status: approved
sensitivity: internal
claim_id: claim-workspace-001
source_type: primary
confidence: high
analyst_status: peer-reviewed
---

# Workspace Claim

Evidence text for workspace indexing.
`
	if err := os.WriteFile(filepath.Join(projectDir, "claim.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}

	p := w.FindProject("project-a")
	if p == nil {
		t.Fatal("project-a was not opened")
	}
	if chunk, ok, err := p.Store.GetSectionChunk("claim.md"); err != nil {
		t.Fatalf("GetSectionChunk: %v", err)
	} else if !ok {
		t.Fatal("expected document-level section chunk")
	} else if chunk.Text == "" || chunk.SectionHash == "" {
		t.Fatalf("expected populated section chunk, got %#v", chunk)
	}

	gov, err := p.Store.GetGovernanceMetadata("claim.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if gov.Status != "approved" || gov.Sensitivity != "internal" {
		t.Fatalf("unexpected governance metadata: %#v", gov)
	}

	research, err := p.Store.GetResearchMetadata("claim.md")
	if err != nil {
		t.Fatalf("GetResearchMetadata: %v", err)
	}
	if research.ClaimID != "claim-workspace-001" || research.Confidence != "high" {
		t.Fatalf("unexpected research metadata: %#v", research)
	}

	if required, found, err := p.Store.GetProjectMeta("reindex_required"); err != nil {
		t.Fatalf("GetProjectMeta: %v", err)
	} else if found && required == "true" {
		t.Fatal("reindex marker should be cleared after full workspace index")
	}
}
