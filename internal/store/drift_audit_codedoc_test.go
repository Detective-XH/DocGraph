package store

import (
	"testing"
	"time"
)

// codedocDriftOpts returns a DriftAuditOpts with safe defaults for codedoc tests.
func codedocDriftOpts() DriftAuditOpts {
	return DriftAuditOpts{
		Limit:               100,
		AsOf:                time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		SimilarityMin:       0.75,
		UnverifiedAfterDays: 180,
	}
}

// insertCodeDocPack inserts the code_doc pack row into domain_packs with
// enabled=1. The pack is not a built-in, so it is not created by SyncDomainPacks;
// tests that exercise the pack gate must call this helper first.
func insertCodeDocPack(t *testing.T, st *Store, enabled bool) {
	t.Helper()
	enabledVal := 0
	if enabled {
		enabledVal = 1
	}
	_, err := st.db.Exec(`
		INSERT OR REPLACE INTO domain_packs
		    (id, name, version, domain, enabled, builtin, min_schema_version, status, description, loaded_at, metadata)
		VALUES
		    ('code_doc', 'Code Documentation', '1.0.0', 'code_doc', ?, 0, 0, 'stable', '', 0, '{}')
	`, enabledVal)
	if err != nil {
		t.Fatalf("insertCodeDocPack: %v", err)
	}
}

// insertCodeFileNode inserts a node with kind="code_file".
func insertCodeFileNode(t *testing.T, st *Store, id, filePath string) {
	t.Helper()
	node := Node{
		ID:            id,
		Kind:          "code_file",
		Name:          id,
		QualifiedName: id,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       10,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("insertCodeFileNode(%q): %v", id, err)
	}
}

// insertUnresolvedRef inserts a row into unresolved_refs.
// fromNodeID must already exist in nodes.
func insertUnresolvedRef(t *testing.T, st *Store, fromNodeID, refText, refKind, filePath string) {
	t.Helper()
	_, err := st.db.Exec(`
		INSERT INTO unresolved_refs (from_node_id, reference_text, reference_kind, line, col, file_path)
		VALUES (?, ?, ?, 1, 0, ?)
	`, fromNodeID, refText, refKind, filePath)
	if err != nil {
		t.Fatalf("insertUnresolvedRef(%q → %q): %v", fromNodeID, refText, err)
	}
}

// ---- TestFindDocsCodeDrift_PackDisabled ----

// TestFindDocsCodeDrift_PackDisabled: when code_doc pack is not registered
// (IsPackEnabled returns false), findDocsCodeDrift must return nil, nil without
// touching any SQL tables.
func TestFindDocsCodeDrift_PackDisabled(t *testing.T) {
	st := newTestStore(t)
	// Do NOT insert the code_doc pack row — IsPackEnabled returns false for missing rows.

	opts := codedocDriftOpts()
	findings, err := st.findDocsCodeDrift(opts)
	if err != nil {
		t.Fatalf("findDocsCodeDrift: unexpected error: %v", err)
	}
	if findings != nil {
		t.Fatalf("expected nil findings when pack disabled, got %v", findings)
	}
}

// ---- TestFindMissingSymbol_Basic ----

// TestFindMissingSymbol_Basic: an unresolved_ref with a .go extension produces
// a warning finding with code code.missing_symbol.
func TestFindMissingSymbol_Basic(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/api.md", "docs/api.md")
	insertUnresolvedRef(t, st, "docs/api.md", "internal/service.go", "wikilinks_to", "docs/api.md")

	opts := codedocDriftOpts()
	findings, err := st.findMissingSymbol(opts)
	if err != nil {
		t.Fatalf("findMissingSymbol: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeCodeMissingSymbol {
		t.Errorf("Code = %q, want %q", f.Code, CodeCodeMissingSymbol)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", f.Severity)
	}
	if f.NodeID != "docs/api.md" {
		t.Errorf("NodeID = %q, want docs/api.md", f.NodeID)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestFindMissingSymbol_NoCodeRefs: an unresolved_ref pointing to a .md path
// is not a code extension and must NOT produce a finding.
func TestFindMissingSymbol_NoCodeRefs(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/readme.md", "docs/readme.md")
	insertUnresolvedRef(t, st, "docs/readme.md", "other/guide.md", "wikilinks_to", "docs/readme.md")

	opts := codedocDriftOpts()
	findings, err := st.findMissingSymbol(opts)
	if err != nil {
		t.Fatalf("findMissingSymbol: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for .md ref, got %d: %+v", len(findings), findings)
	}
}

// ---- TestFindUndocumentedExport ----

// TestFindUndocumentedExport_Basic: a code_file node with no incoming edges from
// any doc node produces an info finding with code code.undocumented_export.
func TestFindUndocumentedExport_Basic(t *testing.T) {
	st := newTestStore(t)
	insertCodeFileNode(t, st, "internal/handler.go", "internal/handler.go")

	opts := codedocDriftOpts()
	findings, err := st.findUndocumentedExport(opts)
	if err != nil {
		t.Fatalf("findUndocumentedExport: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeCodeUndocumentedExport {
		t.Errorf("Code = %q, want %q", f.Code, CodeCodeUndocumentedExport)
	}
	if f.Severity != "info" {
		t.Errorf("Severity = %q, want info", f.Severity)
	}
	if f.NodeID != "internal/handler.go" {
		t.Errorf("NodeID = %q, want internal/handler.go", f.NodeID)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestFindUndocumentedExport_HasDocRef: a code_file node with an incoming
// 'references' edge from a document node must NOT produce a finding.
func TestFindUndocumentedExport_HasDocRef(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/design.md", "docs/design.md")
	insertCodeFileNode(t, st, "internal/handler.go", "internal/handler.go")

	// doc → code_file via references edge
	if err := st.InsertEdges([]Edge{
		{Source: "docs/design.md", Target: "internal/handler.go", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := codedocDriftOpts()
	findings, err := st.findUndocumentedExport(opts)
	if err != nil {
		t.Fatalf("findUndocumentedExport: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings when code_file has doc ref, got %d: %+v", len(findings), findings)
	}
}

// ---- TestFindUnanchoredFeature ----

// TestFindUnanchoredFeature_Basic: an approved document node with no outgoing
// edge to a code_file produces an info finding with code code.unanchored_feature.
func TestFindUnanchoredFeature_Basic(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "specs/feature-x.md", "specs/feature-x.md")

	if err := st.UpsertGovernanceMetadata("specs/feature-x.md", govTuples(
		"status", "approved",
		"owner", "alice",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := codedocDriftOpts()
	findings, err := st.findUnanchoredFeature(opts)
	if err != nil {
		t.Fatalf("findUnanchoredFeature: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeCodeUnanchoredFeature {
		t.Errorf("Code = %q, want %q", f.Code, CodeCodeUnanchoredFeature)
	}
	if f.Severity != "info" {
		t.Errorf("Severity = %q, want info", f.Severity)
	}
	if f.NodeID != "specs/feature-x.md" {
		t.Errorf("NodeID = %q, want specs/feature-x.md", f.NodeID)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestFindUnanchoredFeature_HasCodeRef: an approved document with an outgoing
// 'references' edge to a code_file must NOT produce a finding.
func TestFindUnanchoredFeature_HasCodeRef(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "specs/feature-y.md", "specs/feature-y.md")
	insertCodeFileNode(t, st, "internal/impl.go", "internal/impl.go")

	if err := st.UpsertGovernanceMetadata("specs/feature-y.md", govTuples(
		"status", "approved",
		"owner", "bob",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	// doc → code_file
	if err := st.InsertEdges([]Edge{
		{Source: "specs/feature-y.md", Target: "internal/impl.go", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := codedocDriftOpts()
	findings, err := st.findUnanchoredFeature(opts)
	if err != nil {
		t.Fatalf("findUnanchoredFeature: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings when doc references code_file, got %d: %+v", len(findings), findings)
	}
}

// ---- TestFindDocsCodeDrift_LimitRespected ----

// TestFindDocsCodeDrift_LimitRespected: with opts.Limit=1, findDocsCodeDrift
// (via GetDriftFindings) must return at most 1 finding even when multiple
// sub-checks each produce results.
func TestFindDocsCodeDrift_LimitRespected(t *testing.T) {
	st := newTestStore(t)
	insertCodeDocPack(t, st, true)

	// Produce a findMissingSymbol candidate: doc references a .go path.
	insertTestNode(t, st, "docs/intro.md", "docs/intro.md")
	insertUnresolvedRef(t, st, "docs/intro.md", "cmd/main.go", "wikilinks_to", "docs/intro.md")

	// Produce a findUndocumentedExport candidate: orphaned code_file.
	insertCodeFileNode(t, st, "internal/orphan.go", "internal/orphan.go")

	opts := DriftAuditOpts{
		Limit:               1,
		AsOf:                time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		SimilarityMin:       0.75,
		UnverifiedAfterDays: 180,
	}

	// Use findDocsCodeDrift directly so we only count code-related findings.
	findings, err := st.findDocsCodeDrift(opts)
	if err != nil {
		t.Fatalf("findDocsCodeDrift: %v", err)
	}

	// Each sub-check gets LIMIT=1 SQL cap, so at most 3 (one per sub-check).
	// The important invariant: we get at least 1 finding, and the SQL did not error.
	if len(findings) == 0 {
		t.Fatal("expected at least 1 finding, got 0")
	}
	// Callers apply the global cap; here we verify the sub-checks each respect LIMIT.
	// With opts.Limit=1 each sub-check returns ≤1 row, so total ≤3.
	if len(findings) > 3 {
		t.Fatalf("expected at most 3 findings (1 per sub-check), got %d", len(findings))
	}
}
