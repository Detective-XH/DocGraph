package store

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// assertTablesIntact verifies the nodes table still exists and contains the
// expected number of rows. This is the load-bearing assertion after every
// injection attempt.
func assertTablesIntact(t *testing.T, st *Store, wantNodes int) {
	t.Helper()
	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed after injection attempt: %v", err)
	}
	if stats.NodeCount != wantNodes {
		t.Errorf("expected %d nodes (table intact), got %d", wantNodes, stats.NodeCount)
	}
}

// ---------------------------------------------------------------------------
// SQL Injection Tests
// ---------------------------------------------------------------------------

func TestSQLInjectionSearch(t *testing.T) {
	st := tempStore(t)

	// Seed data so we can verify the table survives each attack.
	node := testNode("safe.md", "document", "Safe Document", "safe.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	payloads := []struct {
		name  string
		query string
	}{
		{"DROP TABLE", `"; DROP TABLE nodes; --`},
		{"OR 1=1", `" OR 1=1 --`},
		{"UNION SELECT", `"' UNION SELECT * FROM files --`},
		{"FTS5 wildcard", `*`},
		{"FTS5 NEAR operator", `NEAR(a b)`},
		{"NULL byte", "\x00"},
	}

	for _, p := range payloads {
		t.Run(p.name, func(t *testing.T) {
			results, err := st.Search(p.query, "", 10)
			// FTS5 may return a syntax error for some payloads — acceptable.
			// The critical thing: no panic, no data loss.
			_ = results
			_ = err

			assertTablesIntact(t, st, 1)
		})
	}
}

func TestSQLInjectionNodeLookup(t *testing.T) {
	st := tempStore(t)

	node := testNode("lookup.md", "document", "Lookup Target", "lookup.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	payloads := []struct {
		name  string
		value string
	}{
		{"DROP TABLE", "'; DROP TABLE nodes; --"},
		{"path traversal", "../../etc/passwd"},
		{"very long string", strings.Repeat("A", 10000)},
		{"NULL byte embedded", "file\x00.md"},
	}

	t.Run("FindNodeByPath", func(t *testing.T) {
		for _, p := range payloads {
			t.Run(p.name, func(t *testing.T) {
				n, err := st.FindNodeByPath(p.value)
				_ = n
				_ = err
				assertTablesIntact(t, st, 1)
			})
		}
	})

	t.Run("FindNodeByName", func(t *testing.T) {
		for _, p := range payloads {
			t.Run(p.name, func(t *testing.T) {
				n, err := st.FindNodeByName(p.value)
				_ = n
				_ = err
				assertTablesIntact(t, st, 1)
			})
		}
	})
}

func TestSQLInjectionEdgeQueries(t *testing.T) {
	st := tempStore(t)

	// Need two nodes for a valid edge.
	nodes := []Node{
		testNode("edge-a.md", "document", "Edge A", "edge-a.md"),
		testNode("edge-b.md", "document", "Edge B", "edge-b.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	edges := []Edge{
		{Source: "edge-a.md", Target: "edge-b.md", Kind: "references"},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	payloads := []struct {
		name  string
		value string
	}{
		{"DELETE injection", "'; DELETE FROM edges; --"},
		{"empty string", ""},
		{"very long string", strings.Repeat("X", 10000)},
	}

	for _, p := range payloads {
		t.Run("GetIncomingEdges/"+p.name, func(t *testing.T) {
			result, err := st.GetIncomingEdges(p.value)
			_ = result
			_ = err
			assertTablesIntact(t, st, 2)

			// Verify the edge table also survives.
			stats, err := st.GetStats()
			if err != nil {
				t.Fatalf("GetStats failed: %v", err)
			}
			if stats.EdgeCount != 1 {
				t.Errorf("expected 1 edge (table intact), got %d", stats.EdgeCount)
			}
		})

		t.Run("GetOutgoingEdges/"+p.name, func(t *testing.T) {
			result, err := st.GetOutgoingEdges(p.value)
			_ = result
			_ = err
			assertTablesIntact(t, st, 2)
		})
	}
}

// ---------------------------------------------------------------------------
// Resource Exhaustion Tests
// ---------------------------------------------------------------------------

func TestSearchQueryLengthCap(t *testing.T) {
	st := tempStore(t)

	// Insert a node whose name appears early in the long query.
	node := testNode("cap.md", "document", "CapTestTarget", "cap.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	// Build a 5000-char query: recognizable token at the front, padding after.
	longQuery := "CapTestTarget " + strings.Repeat("a", 5000-len("CapTestTarget ")-1) + "z"
	if len(longQuery) < 5000 {
		longQuery += strings.Repeat("b", 5000-len(longQuery))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		results, err := st.Search(longQuery, "", 10)
		_ = err
		_ = results
	}()

	select {
	case <-done:
		// Completed without hanging.
	case <-time.After(10 * time.Second):
		t.Fatal("Search with 5000-char query hung for >10s")
	}

	// The store should still be operational.
	assertTablesIntact(t, st, 1)
}

func TestBodyExcerptCap(t *testing.T) {
	st := tempStore(t)

	bigBody := strings.Repeat("X", 10000)
	node := Node{
		ID: "big-body.md", Kind: "document", Name: "Big Body",
		QualifiedName: "big-body.md", FilePath: "big-body.md",
		StartLine: 1, EndLine: 10, Level: 0,
		BodyExcerpt: bigBody, UpdatedAt: 1,
	}
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	stored, err := st.GetNodeByID("big-body.md")
	if err != nil {
		t.Fatalf("GetNodeByID failed: %v", err)
	}
	if stored == nil {
		t.Fatal("expected node, got nil")
	}
	if len(stored.BodyExcerpt) > 500 {
		t.Errorf("BodyExcerpt should be capped at 500 bytes, got %d", len(stored.BodyExcerpt))
	}
}

func TestManyNodesPerformance(t *testing.T) {
	st := tempStore(t)

	const count = 1000
	nodes := make([]Node, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("perf-%d.md", i)
		nodes[i] = Node{
			ID: id, Kind: "document", Name: fmt.Sprintf("Performance Node %d", i),
			QualifiedName: id, FilePath: id,
			StartLine: 1, EndLine: 10, Level: 0,
			BodyExcerpt: fmt.Sprintf("body content for node %d", i), UpdatedAt: 1,
		}
	}

	start := time.Now()
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("inserting %d nodes took %v (limit 5s)", count, elapsed)
	}
	t.Logf("inserted %d nodes in %v", count, elapsed)

	// Verify the count.
	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.NodeCount != count {
		t.Errorf("expected %d nodes, got %d", count, stats.NodeCount)
	}

	// Search still works after mass insert.
	results, err := st.Search("Performance Node 42", "", 10)
	if err != nil {
		t.Fatalf("Search after mass insert failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 search result after mass insert, got 0")
	}
}

// ---------------------------------------------------------------------------
// Edge Cases
// ---------------------------------------------------------------------------

func TestUnicodeExtremes(t *testing.T) {
	st := tempStore(t)

	cases := []struct {
		name string
		id   string
		text string
	}{
		{"RTL Arabic", "rtl.md", "مرحبا بالعالم"},
		{"emoji", "emoji.md", "📊 Dashboard"},
		{"zero-width joiner", "zwj.md", "READ​ME"},
		{"NFC cafe", "cafe-nfc.md", "café"},
		{"NFD cafe", "cafe-nfd.md", "café"},
	}

	// Insert all nodes.
	nodes := make([]Node, len(cases))
	for i, c := range cases {
		nodes[i] = Node{
			ID: c.id, Kind: "document", Name: c.text,
			QualifiedName: c.id, FilePath: c.id,
			StartLine: 1, EndLine: 10, Level: 0,
			BodyExcerpt: c.text, UpdatedAt: 1,
		}
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	// Verify all nodes stored.
	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.NodeCount != len(cases) {
		t.Fatalf("expected %d nodes, got %d", len(cases), stats.NodeCount)
	}

	// Each text must be searchable (at least: no crash; ideally finds itself).
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results, err := st.Search(c.text, "", 10)
			if err != nil {
				// FTS5 trigram may struggle with some Unicode — acceptable.
				t.Logf("Search(%q) returned error (acceptable): %v", c.text, err)
				return
			}
			// We don't mandate a match for all cases (trigram tokenizer has
			// limitations with combining characters), but log what we got.
			t.Logf("Search(%q) returned %d results", c.text, len(results))
		})
	}

	// Verify NFC vs NFD are stored as distinct nodes.
	nfc, err := st.GetNodeByID("cafe-nfc.md")
	if err != nil || nfc == nil {
		t.Fatal("NFC node not found")
	}
	nfd, err := st.GetNodeByID("cafe-nfd.md")
	if err != nil || nfd == nil {
		t.Fatal("NFD node not found")
	}
	if nfc.Name == nfd.Name {
		t.Log("NFC and NFD names stored identically — SQLite/driver normalized them")
	} else {
		t.Log("NFC and NFD names stored as distinct byte sequences")
	}
}

func TestConcurrentReads(t *testing.T) {
	st := tempStore(t)

	// Seed data.
	nodes := make([]Node, 20)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("conc-%d.md", i)
		nodes[i] = Node{
			ID: id, Kind: "document", Name: fmt.Sprintf("Concurrent Doc %d", i),
			QualifiedName: id, FilePath: id,
			StartLine: 1, EndLine: 10, Level: 0,
			BodyExcerpt: fmt.Sprintf("concurrent body %d", i), UpdatedAt: 1,
		}
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Mix of Search and GetStats calls.
			for j := 0; j < 10; j++ {
				query := fmt.Sprintf("Concurrent Doc %d", (id+j)%20)
				results, err := st.Search(query, "", 5)
				if err != nil {
					t.Errorf("goroutine %d: Search(%q) error: %v", id, query, err)
					return
				}
				_ = results

				stats, err := st.GetStats()
				if err != nil {
					t.Errorf("goroutine %d: GetStats error: %v", id, err)
					return
				}
				if stats.NodeCount != 20 {
					t.Errorf("goroutine %d: expected 20 nodes, got %d", id, stats.NodeCount)
				}
			}
		}(i)
	}

	wg.Wait()
}
