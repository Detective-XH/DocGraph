package store

import (
	"testing"
	"time"
)

func TestUpsertAndGetEmbedding(t *testing.T) {
	st := tempStore(t)
	if err := st.InsertNodes([]Node{testNode("doc1.md", "document", "Doc One", "doc1.md")}); err != nil {
		t.Fatal(err)
	}

	vec := []float64{0.1, 0.2, 0.3, 0.4}
	e := Embedding{
		DocID:       "doc1.md",
		ModelID:     "test-model",
		Dim:         len(vec),
		Vector:      vec,
		ContentHash: "abc123",
		UpdatedAt:   time.Now().Unix(),
	}
	if err := st.UpsertEmbedding(e); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}

	got, err := st.GetEmbedding("doc1.md", "test-model")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if got == nil {
		t.Fatal("expected embedding, got nil")
	}
	if got.ContentHash != "abc123" {
		t.Errorf("ContentHash: got %q want %q", got.ContentHash, "abc123")
	}
	if len(got.Vector) != len(vec) {
		t.Fatalf("Vector len: got %d want %d", len(got.Vector), len(vec))
	}
	for i := range vec {
		if got.Vector[i] != vec[i] {
			t.Errorf("Vector[%d]: got %f want %f", i, got.Vector[i], vec[i])
		}
	}
}

func TestUpsertEmbeddingOverwrite(t *testing.T) {
	st := tempStore(t)
	st.InsertNodes([]Node{testNode("doc1.md", "document", "Doc One", "doc1.md")})

	e1 := Embedding{DocID: "doc1.md", ModelID: "m", Dim: 2, Vector: []float64{1, 0}, ContentHash: "h1"}
	st.UpsertEmbedding(e1)

	e2 := Embedding{DocID: "doc1.md", ModelID: "m", Dim: 2, Vector: []float64{0, 1}, ContentHash: "h2"}
	if err := st.UpsertEmbedding(e2); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetEmbedding("doc1.md", "m")
	if got.ContentHash != "h2" {
		t.Errorf("expected overwritten hash h2, got %s", got.ContentHash)
	}
	if got.Vector[0] != 0 || got.Vector[1] != 1 {
		t.Errorf("expected overwritten vector [0 1], got %v", got.Vector)
	}
}

func TestGetEmbeddingNotFound(t *testing.T) {
	st := tempStore(t)
	got, err := st.GetEmbedding("nonexistent", "model")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetPendingEmbeddings(t *testing.T) {
	st := tempStore(t)
	nodes := []Node{
		testNode("a.md", "document", "A", "a.md"),
		testNode("b.md", "document", "B", "b.md"),
		testNode("c.md", "heading", "C Heading", "a.md"), // non-document, should be excluded
	}
	st.InsertNodes(nodes)

	// Insert a file record for a.md so content_hash comparison works.
	st.db.Exec(`INSERT INTO files (path, content_hash, size, modified_at, indexed_at) VALUES ('a.md', 'hash-a', 100, 1, 1)`)
	st.db.Exec(`INSERT INTO files (path, content_hash, size, modified_at, indexed_at) VALUES ('b.md', 'hash-b', 100, 1, 1)`)

	// Both docs pending (no embeddings yet).
	docs, err := st.GetPendingEmbeddings("model-x", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(docs))
	}

	// Store embedding for a.md with current content_hash — should no longer be pending.
	st.UpsertEmbedding(Embedding{DocID: "a.md", ModelID: "model-x", Dim: 2, Vector: []float64{1, 0}, ContentHash: "hash-a"})

	docs, err = st.GetPendingEmbeddings("model-x", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 pending after storing a.md, got %d", len(docs))
	}
	if docs[0].DocID != "b.md" {
		t.Errorf("expected b.md pending, got %s", docs[0].DocID)
	}
}

func TestGetPendingEmbeddingsStale(t *testing.T) {
	st := tempStore(t)
	st.InsertNodes([]Node{testNode("a.md", "document", "A", "a.md")})
	st.db.Exec(`INSERT INTO files (path, content_hash, size, modified_at, indexed_at) VALUES ('a.md', 'new-hash', 100, 1, 1)`)

	// Store embedding with OLD content_hash — should appear as stale (pending).
	st.UpsertEmbedding(Embedding{DocID: "a.md", ModelID: "model-x", Dim: 2, Vector: []float64{1, 0}, ContentHash: "old-hash"})

	docs, err := st.GetPendingEmbeddings("model-x", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 stale doc, got %d", len(docs))
	}
}

func TestDeleteEmbeddingsByModel(t *testing.T) {
	st := tempStore(t)
	st.InsertNodes([]Node{
		testNode("a.md", "document", "A", "a.md"),
		testNode("b.md", "document", "B", "b.md"),
	})

	st.UpsertEmbedding(Embedding{DocID: "a.md", ModelID: "m1", Dim: 1, Vector: []float64{1}, ContentHash: "h"})
	st.UpsertEmbedding(Embedding{DocID: "b.md", ModelID: "m1", Dim: 1, Vector: []float64{1}, ContentHash: "h"})
	st.UpsertEmbedding(Embedding{DocID: "a.md", ModelID: "m2", Dim: 1, Vector: []float64{1}, ContentHash: "h"})

	n, err := st.DeleteEmbeddingsByModel("m1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 deleted, got %d", n)
	}

	// m2 should still exist.
	got, _ := st.GetEmbedding("a.md", "m2")
	if got == nil {
		t.Error("m2 embedding for a.md should still exist")
	}
	// m1 should be gone.
	gone, _ := st.GetEmbedding("a.md", "m1")
	if gone != nil {
		t.Error("m1 embedding for a.md should be deleted")
	}
}

func TestGetEmbeddingModelStats(t *testing.T) {
	st := tempStore(t)
	st.InsertNodes([]Node{
		testNode("a.md", "document", "A", "a.md"),
		testNode("b.md", "document", "B", "b.md"),
	})
	st.db.Exec(`INSERT INTO files (path, content_hash, size, modified_at, indexed_at) VALUES ('a.md', 'hash-a', 100, 1, 1)`)
	st.db.Exec(`INSERT INTO files (path, content_hash, size, modified_at, indexed_at) VALUES ('b.md', 'hash-b', 100, 1, 1)`)

	// a.md: fresh; b.md: stale (stored with old hash)
	st.UpsertEmbedding(Embedding{DocID: "a.md", ModelID: "m1", Dim: 1, Vector: []float64{1}, ContentHash: "hash-a"})
	st.UpsertEmbedding(Embedding{DocID: "b.md", ModelID: "m1", Dim: 1, Vector: []float64{1}, ContentHash: "old-hash"})

	stats, err := st.GetEmbeddingModelStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 model stat, got %d", len(stats))
	}
	s := stats[0]
	if s.ModelID != "m1" {
		t.Errorf("ModelID: got %q want %q", s.ModelID, "m1")
	}
	if s.Total != 2 {
		t.Errorf("Total: got %d want 2", s.Total)
	}
	if s.Stale != 1 {
		t.Errorf("Stale: got %d want 1", s.Stale)
	}
}
