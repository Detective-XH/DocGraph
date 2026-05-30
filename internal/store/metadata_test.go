package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestStore opens a fresh in-memory-equivalent SQLite DB in a temp dir.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// insertTestNode inserts a document node with the given id/filePath.
func insertTestNode(t *testing.T, st *Store, id, filePath string) {
	t.Helper()
	node := Node{
		ID:            id,
		Kind:          "document",
		Name:          id,
		QualifiedName: id,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       10,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes(%q): %v", id, err)
	}
}

// ---- InsertDocumentMetadata ----

func TestInsertDocumentMetadata_Basic(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/a.md", "doc/a.md")

	tuples := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "owner", Value: "alice", ValueType: "string", Source: "frontmatter"},
		{Key: "department", Value: "engineering", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("doc/a.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}

	got, err := st.GetDocumentMetadata("doc/a.md")
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 tuples, got %d", len(got))
	}
}

func TestInsertDocumentMetadata_AuditTrail(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/b.md", "doc/b.md")

	// Insert same key from two different sources — both rows must coexist.
	tuples := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "status", Value: "draft", ValueType: "string", Source: "skill_advisory"},
	}
	if err := st.InsertDocumentMetadata("doc/b.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}

	got, err := st.GetDocumentMetadata("doc/b.md")
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (audit trail model), got %d", len(got))
	}

	// Verify both sources are present.
	sources := make(map[string]string)
	for _, g := range got {
		sources[g.Source] = g.Value
	}
	if sources["frontmatter"] != "approved" {
		t.Errorf("expected frontmatter value=approved, got %q", sources["frontmatter"])
	}
	if sources["skill_advisory"] != "draft" {
		t.Errorf("expected skill_advisory value=draft, got %q", sources["skill_advisory"])
	}
}

func TestInsertDocumentMetadata_InvalidSource(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/c.md", "doc/c.md")

	tuples := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "invalid"},
	}
	err := st.InsertDocumentMetadata("doc/c.md", tuples)
	if err == nil {
		t.Fatal("expected error for invalid source, got nil")
	}
	if !strings.Contains(err.Error(), "invalid source") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestInsertDocumentMetadata_TupleCapEnforced(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/d.md", "doc/d.md")

	tuples := make([]MetadataTuple, 201)
	for i := range tuples {
		tuples[i] = MetadataTuple{Key: "k", Value: "v", ValueType: "string", Source: "frontmatter"}
	}
	err := st.InsertDocumentMetadata("doc/d.md", tuples)
	if err == nil {
		t.Fatal("expected error for 201 tuples, got nil")
	}
	if !strings.Contains(err.Error(), "cap") && !strings.Contains(err.Error(), "201") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestInsertDocumentMetadata_SameSourceUpdate(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/e.md", "doc/e.md")

	// First insert.
	if err := st.InsertDocumentMetadata("doc/e.md", []MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second insert with the same key+source — value should be updated.
	if err := st.InsertDocumentMetadata("doc/e.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("second insert: %v", err)
	}

	got, err := st.GetDocumentMetadata("doc/e.md")
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].Value != "approved" {
		t.Errorf("expected updated value=approved, got %q", got[0].Value)
	}
}

// ---- UpsertGovernanceMetadata ----

func TestUpsertGovernanceMetadata_AuthorityOrdering(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/f.md", "doc/f.md")

	// Insert both sources into document_metadata first (audit trail).
	tuples := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "status", Value: "draft", ValueType: "string", Source: "skill_advisory"},
	}
	if err := st.InsertDocumentMetadata("doc/f.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}

	// UpsertGovernanceMetadata should resolve to frontmatter (priority 4 > 1).
	if err := st.UpsertGovernanceMetadata("doc/f.md", tuples); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	rec, err := st.GetGovernanceMetadata("doc/f.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if rec == nil {
		t.Fatal("expected governance record, got nil")
	}
	if rec.Status != "approved" {
		t.Errorf("expected Status=approved (frontmatter wins), got %q", rec.Status)
	}
}

func TestUpsertGovernanceMetadata_AllFields(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/g.md", "doc/g.md")

	tuples := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "owner", Value: "alice", ValueType: "string", Source: "frontmatter"},
		{Key: "approver", Value: "bob", ValueType: "string", Source: "frontmatter"},
		{Key: "department", Value: "engineering", ValueType: "string", Source: "frontmatter"},
		{Key: "effective_date", Value: "2025-01-01", ValueType: "date", Source: "frontmatter"},
		{Key: "review_due", Value: "2026-01-01", ValueType: "date", Source: "frontmatter"},
		{Key: "supersedes", Value: "old-doc.md", ValueType: "string", Source: "frontmatter"},
		{Key: "superseded_by", Value: "new-doc.md", ValueType: "string", Source: "frontmatter"},
		{Key: "sensitivity", Value: "internal", ValueType: "string", Source: "frontmatter"},
		{Key: "allowed_audience", Value: "employees", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "https://example.com/doc", ValueType: "string", Source: "frontmatter"},
	}

	if err := st.InsertDocumentMetadata("doc/g.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("doc/g.md", tuples); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	rec, err := st.GetGovernanceMetadata("doc/g.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if rec == nil {
		t.Fatal("expected governance record, got nil")
	}

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"Status", rec.Status, "approved"},
		{"Owner", rec.Owner, "alice"},
		{"Approver", rec.Approver, "bob"},
		{"Department", rec.Department, "engineering"},
		{"EffectiveDate", rec.EffectiveDate, "2025-01-01"},
		{"ReviewDue", rec.ReviewDue, "2026-01-01"},
		{"Supersedes", rec.Supersedes, "old-doc.md"},
		{"SupersededBy", rec.SupersededBy, "new-doc.md"},
		{"Sensitivity", rec.Sensitivity, "internal"},
		{"AllowedAudience", rec.AllowedAudience, "employees"},
		{"CanonicalSource", rec.CanonicalSource, "https://example.com/doc"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
}

func TestUpsertGovernanceMetadata_NoGovernanceKeys(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/h.md", "doc/h.md")

	// Only non-governance keys.
	tuples := []MetadataTuple{
		{Key: "custom_field", Value: "something", ValueType: "string", Source: "frontmatter"},
		{Key: "other_field", Value: "else", ValueType: "string", Source: "extractor"},
	}
	if err := st.InsertDocumentMetadata("doc/h.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("doc/h.md", tuples); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	rec, err := st.GetGovernanceMetadata("doc/h.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil governance record for non-governance keys, got %+v", rec)
	}
}

// ---- DeleteDocumentMetadataByFile ----

func TestDeleteDocumentMetadataByFile(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/keep.md", "doc/keep.md")
	insertTestNode(t, st, "doc/delete.md", "doc/delete.md")

	tuplesKeep := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
	}
	tuplesDelete := []MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
	}

	if err := st.InsertDocumentMetadata("doc/keep.md", tuplesKeep); err != nil {
		t.Fatalf("InsertDocumentMetadata keep: %v", err)
	}
	if err := st.InsertDocumentMetadata("doc/delete.md", tuplesDelete); err != nil {
		t.Fatalf("InsertDocumentMetadata delete: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("doc/delete.md", tuplesDelete); err != nil {
		t.Fatalf("UpsertGovernanceMetadata delete: %v", err)
	}
	if err := st.UpsertResearchMetadata("doc/delete.md", []MetadataTuple{
		{Key: "claim_id", Value: "claim-delete", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata delete: %v", err)
	}

	// Delete the second file's metadata.
	if err := st.DeleteDocumentMetadataByFile("doc/delete.md"); err != nil {
		t.Fatalf("DeleteDocumentMetadataByFile: %v", err)
	}

	// Verify the deleted file has no metadata.
	gotDeleted, err := st.GetDocumentMetadata("doc/delete.md")
	if err != nil {
		t.Fatalf("GetDocumentMetadata deleted: %v", err)
	}
	if len(gotDeleted) != 0 {
		t.Errorf("expected 0 tuples for deleted file, got %d", len(gotDeleted))
	}
	govDeleted, err := st.GetGovernanceMetadata("doc/delete.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata deleted: %v", err)
	}
	if govDeleted != nil {
		t.Errorf("expected deleted governance projection to be nil, got %+v", govDeleted)
	}
	researchDeleted, err := st.GetResearchMetadata("doc/delete.md")
	if err != nil {
		t.Fatalf("GetResearchMetadata deleted: %v", err)
	}
	if researchDeleted != nil {
		t.Errorf("expected deleted research projection to be nil, got %+v", researchDeleted)
	}

	// Verify the kept file still has its metadata.
	gotKeep, err := st.GetDocumentMetadata("doc/keep.md")
	if err != nil {
		t.Fatalf("GetDocumentMetadata keep: %v", err)
	}
	if len(gotKeep) != 1 {
		t.Errorf("expected 1 tuple for kept file, got %d", len(gotKeep))
	}
}

// ---- GetGovernanceMetadata ----

func TestGetGovernanceMetadata_NotFound(t *testing.T) {
	st := newTestStore(t)

	rec, err := st.GetGovernanceMetadata("nonexistent/node.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil for nonexistent node, got %+v", rec)
	}
}

// ---- GetMetadataStats ----

func TestGetMetadataStats(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/s1.md", "doc/s1.md")
	insertTestNode(t, st, "doc/s2.md", "doc/s2.md")

	tuples := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("doc/s1.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata s1: %v", err)
	}
	if err := st.InsertDocumentMetadata("doc/s2.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata s2: %v", err)
	}

	stats, err := st.GetMetadataStats()
	if err != nil {
		t.Fatalf("GetMetadataStats: %v", err)
	}
	if stats.TotalDocs < 2 {
		t.Errorf("TotalDocs: expected >= 2, got %d", stats.TotalDocs)
	}
	if stats.DocsWithMetadata != 2 {
		t.Errorf("DocsWithMetadata: expected 2, got %d", stats.DocsWithMetadata)
	}
}

// ---- UpsertResearchMetadata ----

func TestUpsertResearchMetadata_AuthorityOrdering(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/research-authority.md", "doc/research-authority.md")

	tuples := []MetadataTuple{
		{Key: "claim_id", Value: "claim-001", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "low", ValueType: "string", Source: "skill_advisory"},
		{Key: "source_type", Value: "secondary", ValueType: "string", Source: "skill_advisory"},
	}
	if err := st.InsertDocumentMetadata("doc/research-authority.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}
	if err := st.UpsertResearchMetadata("doc/research-authority.md", tuples); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	rec, err := st.GetResearchMetadata("doc/research-authority.md")
	if err != nil {
		t.Fatalf("GetResearchMetadata: %v", err)
	}
	if rec == nil {
		t.Fatal("expected research record, got nil")
	}
	if rec.Confidence != "high" {
		t.Errorf("expected frontmatter confidence=high to win, got %q", rec.Confidence)
	}
	if rec.SourceType != "secondary" {
		t.Errorf("expected advisory-only source_type=secondary, got %q", rec.SourceType)
	}
}

func TestUpsertResearchMetadata_AllFields(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/research-all.md", "doc/research-all.md")

	tuples := []MetadataTuple{
		{Key: "claim_id", Value: "claim-alpha-001", ValueType: "string", Source: "frontmatter"},
		{Key: "evidence", Value: `["[[source-brief.md]]","https://example.com/report"]`, ValueType: "list", Source: "frontmatter"},
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		{Key: "event_date", Value: "2026-05-01", ValueType: "date", Source: "frontmatter"},
		{Key: "assessment_date", Value: "2026-05-20", ValueType: "date", Source: "frontmatter"},
		{Key: "last_verified", Value: "2026-05-25", ValueType: "date", Source: "frontmatter"},
		{Key: "valid_until", Value: "2026-08-01", ValueType: "date", Source: "frontmatter"},
		{Key: "analyst_status", Value: "peer-reviewed", ValueType: "string", Source: "frontmatter"},
		{Key: "client", Value: "example-client", ValueType: "string", Source: "frontmatter"},
		{Key: "deliverable_id", Value: "deliverable-2026-05", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("doc/research-all.md", tuples); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}
	if err := st.UpsertResearchMetadata("doc/research-all.md", tuples); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	rec, err := st.GetResearchMetadata("doc/research-all.md")
	if err != nil {
		t.Fatalf("GetResearchMetadata: %v", err)
	}
	if rec == nil {
		t.Fatal("expected research record, got nil")
	}
	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"ClaimID", rec.ClaimID, "claim-alpha-001"},
		{"Evidence", rec.Evidence, `["[[source-brief.md]]","https://example.com/report"]`},
		{"SourceType", rec.SourceType, "primary"},
		{"Confidence", rec.Confidence, "high"},
		{"EventDate", rec.EventDate, "2026-05-01"},
		{"AssessmentDate", rec.AssessmentDate, "2026-05-20"},
		{"LastVerified", rec.LastVerified, "2026-05-25"},
		{"ValidUntil", rec.ValidUntil, "2026-08-01"},
		{"AnalystStatus", rec.AnalystStatus, "peer-reviewed"},
		{"Client", rec.Client, "example-client"},
		{"DeliverableID", rec.DeliverableID, "deliverable-2026-05"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
}

// ---- IsGovernanceEmpty ----

func TestIsGovernanceEmpty(t *testing.T) {
	tests := []struct {
		name string
		rec  *GovernanceRecord
		want bool
	}{
		{
			name: "nil returns true",
			rec:  nil,
			want: true,
		},
		{
			name: "all empty fields returns true",
			rec:  &GovernanceRecord{},
			want: true,
		},
		{
			name: "NodeID only (no governance fields) returns true",
			rec:  &GovernanceRecord{NodeID: "doc/x.md"},
			want: true,
		},
		{
			name: "status set returns false",
			rec:  &GovernanceRecord{Status: "approved"},
			want: false,
		},
		{
			name: "owner set returns false",
			rec:  &GovernanceRecord{Owner: "alice"},
			want: false,
		},
		{
			name: "sensitivity set returns false",
			rec:  &GovernanceRecord{Sensitivity: "internal"},
			want: false,
		},
		{
			name: "canonical_source set returns false",
			rec:  &GovernanceRecord{CanonicalSource: "https://example.com"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsGovernanceEmpty(tc.rec)
			if got != tc.want {
				t.Errorf("IsGovernanceEmpty(%+v) = %v, want %v", tc.rec, got, tc.want)
			}
		})
	}
}

func TestIsResearchEmpty(t *testing.T) {
	tests := []struct {
		name string
		rec  *ResearchRecord
		want bool
	}{
		{name: "nil returns true", rec: nil, want: true},
		{name: "all empty fields returns true", rec: &ResearchRecord{}, want: true},
		{name: "claim id set returns false", rec: &ResearchRecord{ClaimID: "claim"}, want: false},
		{name: "confidence set returns false", rec: &ResearchRecord{Confidence: "high"}, want: false},
		{name: "evidence set returns false", rec: &ResearchRecord{Evidence: "[]"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsResearchEmpty(tc.rec)
			if got != tc.want {
				t.Errorf("IsResearchEmpty(%+v) = %v, want %v", tc.rec, got, tc.want)
			}
		})
	}
}
