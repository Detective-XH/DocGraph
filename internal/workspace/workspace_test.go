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

	assertSectionChunkPopulated(t, p, "claim.md")
	assertGovernanceMetadata(t, p, "claim.md", "approved", "internal")
	assertResearchMetadata(t, p, "claim.md", "claim-workspace-001", "high")
	assertReindexCleared(t, p)
}

// assertSectionChunkPopulated verifies that a document-level section chunk
// exists for docPath and has non-empty Text and SectionHash fields.
func assertSectionChunkPopulated(t *testing.T, p *Project, docPath string) {
	t.Helper()
	chunk, ok, err := p.Store.GetSectionChunk(docPath)
	if err != nil {
		t.Fatalf("GetSectionChunk: %v", err)
	}
	if !ok {
		t.Fatal("expected document-level section chunk")
	}
	if chunk.Text == "" || chunk.SectionHash == "" {
		t.Fatalf("expected populated section chunk, got %#v", chunk)
	}
}

// assertGovernanceMetadata verifies the governance fields for docPath.
func assertGovernanceMetadata(t *testing.T, p *Project, docPath, wantStatus, wantSensitivity string) {
	t.Helper()
	gov, err := p.Store.GetGovernanceMetadata(docPath)
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if gov.Status != wantStatus || gov.Sensitivity != wantSensitivity {
		t.Fatalf("unexpected governance metadata: %#v", gov)
	}
}

// assertResearchMetadata verifies the research fields for docPath.
func assertResearchMetadata(t *testing.T, p *Project, docPath, wantClaimID, wantConfidence string) {
	t.Helper()
	research, err := p.Store.GetResearchMetadata(docPath)
	if err != nil {
		t.Fatalf("GetResearchMetadata: %v", err)
	}
	if research.ClaimID != wantClaimID || research.Confidence != wantConfidence {
		t.Fatalf("unexpected research metadata: %#v", research)
	}
}

// assertReindexCleared verifies the reindex_required project meta is not set to "true".
func assertReindexCleared(t *testing.T, p *Project) {
	t.Helper()
	required, found, err := p.Store.GetProjectMeta("reindex_required")
	if err != nil {
		t.Fatalf("GetProjectMeta: %v", err)
	}
	if found && required == "true" {
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
	assertNoCodeNodes(t, p, "service.go")
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
	assertCodeFileNode(t, p, "service.go")
	assertDocCommentChunk(t, p, "service.go#doc_comment-3", "Handler handles workspace requests.")
}

// assertNoCodeNodes verifies that no nodes exist for a code file when code_doc is disabled.
func assertNoCodeNodes(t *testing.T, p *Project, file string) {
	t.Helper()
	nodes, err := p.Store.GetNodesByFile(file)
	if err != nil {
		t.Fatalf("GetNodesByFile disabled: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("code_doc disabled should skip code files, got nodes: %#v", nodes)
	}
}

// assertCodeFileNode verifies that a code_file node exists for file.
func assertCodeFileNode(t *testing.T, p *Project, file string) {
	t.Helper()
	nodes, err := p.Store.GetNodesByFile(file)
	if err != nil {
		t.Fatalf("GetNodesByFile enabled: %v", err)
	}
	if len(nodes) == 0 || nodes[0].Kind != "code_file" {
		t.Fatalf("code_doc enabled should index a code_file node, got %#v", nodes)
	}
}

// assertDocCommentChunk verifies that a section chunk for anchor contains wantSubstr.
func assertDocCommentChunk(t *testing.T, p *Project, anchor, wantSubstr string) {
	t.Helper()
	chunk, ok, err := p.Store.GetSectionChunk(anchor)
	if err != nil {
		t.Fatalf("GetSectionChunk: %v", err)
	}
	if !ok || !strings.Contains(chunk.Text, wantSubstr) {
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
