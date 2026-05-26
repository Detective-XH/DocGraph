package workspace

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
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

func TestIndexAllRespectsCodeDocPack(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	src := `package service

// Handler handles workspace requests.
func Handler() {}
`
	if err := os.WriteFile(filepath.Join(projectDir, "service.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	p := w.FindProject("project-a")
	if p == nil {
		t.Fatal("project-a was not opened")
	}
	if nodes, err := p.Store.GetNodesByFile("service.go"); err != nil {
		t.Fatalf("GetNodesByFile disabled: %v", err)
	} else if len(nodes) != 0 {
		t.Fatalf("code_doc disabled should skip code files, got nodes: %#v", nodes)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	enableCodeDocPack(t, projectDir)

	w, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	p = w.FindProject("project-a")
	if p == nil {
		t.Fatal("project-a was not opened after enabling code_doc")
	}
	nodes, err := p.Store.GetNodesByFile("service.go")
	if err != nil {
		t.Fatalf("GetNodesByFile enabled: %v", err)
	}
	if len(nodes) == 0 || nodes[0].Kind != "code_file" {
		t.Fatalf("code_doc enabled should index a code_file node, got %#v", nodes)
	}

	chunk, ok, err := p.Store.GetSectionChunk("service.go#doc_comment-3")
	if err != nil {
		t.Fatalf("GetSectionChunk: %v", err)
	}
	if !ok || !strings.Contains(chunk.Text, "Handler handles workspace requests.") {
		t.Fatalf("expected Go doc comment section chunk, got ok=%v chunk=%#v", ok, chunk)
	}
}

func enableCodeDocPack(t *testing.T, projectDir string) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(projectDir, ".docgraph", "docgraph.db"))
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE domain_packs SET enabled = 1 WHERE id = 'code_doc'`)
	if err != nil {
		t.Fatalf("enable code_doc pack: %v", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("code_doc rows affected: %v", err)
	}
	if affected != 1 {
		t.Fatalf("code_doc pack row missing; rows affected = %d", affected)
	}
}
