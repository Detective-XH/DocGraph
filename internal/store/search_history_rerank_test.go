package store

import (
	"math"
	"testing"
)

// assertSearchOrder locates idA and idZ in results and asserts their rank and
// position semantics. wantTied=true requires equal Rank values. wantAFirst=true
// requires idA at a lower index than idZ; false requires idZ before idA. label
// is a short phase name used in failure messages (e.g. "phase 1").
func assertSearchOrder(t *testing.T, results []SearchResult, idA, idZ, label string, wantTied, wantAFirst bool) {
	t.Helper()
	ia, iz := indexOfDoc(results, idA), indexOfDoc(results, idZ)
	if ia < 0 || iz < 0 {
		t.Fatalf("%s: both docs must be present, got a=%d z=%d", label, ia, iz)
	}
	if wantTied && results[ia].Rank != results[iz].Rank {
		t.Fatalf("%s: twins must tie, got %s=%.6f %s=%.6f", label, idA, results[ia].Rank, idZ, results[iz].Rank)
	}
	if wantAFirst && ia >= iz {
		t.Fatalf("%s: expected %s before %s (a=%d, z=%d)", label, idA, idZ, ia, iz)
	}
	if !wantAFirst && !wantTied && iz >= ia {
		t.Fatalf("%s: expected %s before %s (a=%d, z=%d)", label, idZ, idA, ia, iz)
	}
}

// TestHistoryRerankActivatesOnChurn verifies applyHistoryReranking is wired but
// contributes nothing without git history, and proves it DOES activate —
// flipping result order — once one twin carries high git churn. Churn is used as
// an importance proxy (commit_count + author_count), so the doc edited more often
// by more people sorts higher.
//
// Isolation mirrors TestGraphRerankActivatesOnLinkDensity: aaaaa.md and zzzzz.md
// are identical in every field the text ranker reads AND equal-length (8 chars →
// equal trigram count → equal bm25 doc-length norm over qualified_name), so with
// no/equal history they TIE and FilePath-ascending puts aaaaa.md first. The ONLY
// thing that changes across phases is file_history rows, and applyHistoryReranking
// is the sole scorer that reads them — any order change is attributable to it.
//
// Three phases make this stronger than the graph test's two:
//   - Phase 1 (no history): tie — the inert-when-absent proof.
//   - Phase 2 (EQUAL history on both): still a tie — proves the signal is
//     symmetric/deterministic, so only an asymmetry can flip order (not the mere
//     presence of history).
//   - Phase 3 (one twin bumped higher): the delta flips it — the with-signal proof.
func TestHistoryRerankActivatesOnChurn(t *testing.T) {
	st := tempStore(t)

	const body = "telemetry metrics overview dashboard"
	twins := []Node{
		{ID: "aaaaa.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "aaaaa.md",
			FilePath: "aaaaa.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
		{ID: "zzzzz.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "zzzzz.md",
			FilePath: "zzzzz.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
	}
	if err := st.InsertNodes(twins); err != nil {
		t.Fatalf("InsertNodes twins: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("aaaaa.md", "aaaaa.md", "h1", "doc", "", body, 1, 5),
		sectionChunk("zzzzz.md", "zzzzz.md", "h1", "doc", "", body, 1, 5),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	// Phase 1: no file_history. Tie → FilePath tie-break → aaaaa.md before zzzzz.md.
	base, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("phase 1 Search: %v", err)
	}
	assertSearchOrder(t, base, "aaaaa.md", "zzzzz.md", "phase 1", true, true)

	// Phase 2: EQUAL history on both twins. The boost is identical, so the tie and
	// the FilePath order must survive — presence of history alone changes nothing.
	for _, id := range []string{"aaaaa.md", "zzzzz.md"} {
		if err := st.UpsertFileHistory(FileHistory{
			Path: id, CommitCount: 5, AuthorCount: 2, LastCommitAt: 1,
		}); err != nil {
			t.Fatalf("UpsertFileHistory(%s): %v", id, err)
		}
	}
	eq, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("phase 2 Search: %v", err)
	}
	assertSearchOrder(t, eq, "aaaaa.md", "zzzzz.md", "phase 2", true, true)

	// Phase 3: bump ONLY zzzzz.md to high churn (near the cap). aaaaa.md keeps its
	// phase-2 history, so the delta in churn boost is the sole cause of any flip.
	if err := st.UpsertFileHistory(FileHistory{
		Path: "zzzzz.md", CommitCount: 50, AuthorCount: 20, LastCommitAt: 1,
	}); err != nil {
		t.Fatalf("phase 3 UpsertFileHistory: %v", err)
	}
	treat, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("phase 3 Search: %v", err)
	}
	ja, jz := indexOfDoc(treat, "aaaaa.md"), indexOfDoc(treat, "zzzzz.md")
	if ja < 0 || jz < 0 {
		t.Fatalf("phase 3: both docs must be present, got a=%d z=%d", ja, jz)
	}
	if jz >= ja {
		t.Fatalf("history rerank did NOT activate: zzzzz.md has commit_count=50/author_count=20 but "+
			"did not overtake aaaaa.md (a=%d, z=%d). The feature is wired but ineffective.", ja, jz)
	}
	ia, iz := indexOfDoc(base, "aaaaa.md"), indexOfDoc(base, "zzzzz.md")
	t.Logf("history rerank verified: high churn flipped zzzzz.md from #%d→#%d (aaaaa #%d→#%d)", iz, jz, ia, ja)
}

// TestHistoryRerankDoesNotBuryStrongTextMatch is the calibration guard the flip
// test cannot give: it proves the churn boost stays SUBORDINATE to clear text
// relevance. A doc that matches the query strongly on text (name + body) and has
// ZERO git history must still outrank a doc that barely matches (body only) even
// when that doc carries maximal churn. Because the boost is both log-compressed
// AND capped, the realizable churn contribution is small (≈17 uncapped, ≤10
// capped) — far below the name-field text gap — so the strong match holds. The
// guard's real teeth: it FAILS loudly if a future change drops the log-compression
// or cap (e.g. a raw commit_count multiplier), which is exactly when churn would
// start burying authoritative-but-stable docs. It does not pin the exact cap value
// (the flip test's near-cap values do that); it pins the "history never dominates
// clear text relevance" invariant.
func TestHistoryRerankDoesNotBuryStrongTextMatch(t *testing.T) {
	st := tempStore(t)

	// strong.md: query term in the Name (highest-weight field) AND body → large text score, no history.
	// weak.md:   query term only in the body → small text score, but maximal churn.
	nodes := []Node{
		{ID: "strong.md", Kind: "document", Name: "Telemetry Runbook", QualifiedName: "strong.md",
			FilePath: "strong.md", StartLine: 1, EndLine: 5, BodyExcerpt: "telemetry operational guide", UpdatedAt: 1},
		{ID: "weak.md", Kind: "document", Name: "Quarterly Notes", QualifiedName: "weak.md",
			FilePath: "weak.md", StartLine: 1, EndLine: 5, BodyExcerpt: "telemetry mentioned once here", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("strong.md", "strong.md", "h1", "doc", "Telemetry Runbook", "telemetry operational guide", 1, 5),
		sectionChunk("weak.md", "weak.md", "h2", "doc", "Quarterly Notes", "telemetry mentioned once here", 1, 5),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}
	// weak.md carries maximal churn (both terms saturate their caps); strong.md has none.
	if err := st.UpsertFileHistory(FileHistory{
		Path: "weak.md", CommitCount: 99, AuthorCount: 40, LastCommitAt: 1,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	results, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	is, iw := indexOfDoc(results, "strong.md"), indexOfDoc(results, "weak.md")
	if is < 0 || iw < 0 {
		t.Fatalf("both docs must be present, got strong=%d weak=%d", is, iw)
	}
	if is >= iw {
		t.Fatalf("churn buried the strong text match: weak.md (body-only + max churn) outranked "+
			"strong.md (name+body match, no churn) — strong=#%d weak=#%d. The history signal must "+
			"stay subordinate to clear text relevance (log-compression or cap removed?).", is, iw)
	}
	t.Logf("calibration guard held: strong text match #%d stays above weak+max-churn #%d", is, iw)
}

// TestHistoryRerankAuthorCountContributes isolates the SECOND scored term. The
// flip test above saturates the commit term, which alone flips the tie — so a
// regression that dropped or zeroed the author-count contribution would pass it
// silently. Here both twins carry EQUAL commit_count (the commit boost is
// identical and cancels) and differ ONLY in author_count, so the flip is
// attributable to the author term alone. If that term dies, the twins stay tied
// and this test fails loudly — closing the gap where half the churn proxy
// (per-author breadth) could regress untested.
func TestHistoryRerankAuthorCountContributes(t *testing.T) {
	st := tempStore(t)

	const body = "telemetry metrics overview dashboard"
	twins := []Node{
		{ID: "aaaaa.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "aaaaa.md",
			FilePath: "aaaaa.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
		{ID: "zzzzz.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "zzzzz.md",
			FilePath: "zzzzz.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
	}
	if err := st.InsertNodes(twins); err != nil {
		t.Fatalf("InsertNodes twins: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("aaaaa.md", "aaaaa.md", "h1", "doc", "", body, 1, 5),
		sectionChunk("zzzzz.md", "zzzzz.md", "h1", "doc", "", body, 1, 5),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	base, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("baseline Search: %v", err)
	}
	if ia, iz := indexOfDoc(base, "aaaaa.md"), indexOfDoc(base, "zzzzz.md"); base[ia].Rank != base[iz].Rank || ia >= iz {
		t.Fatalf("baseline: twins must tie with aaaaa.md first to isolate the author term, got a=#%d(%.6f) z=#%d(%.6f)",
			ia, base[ia].Rank, iz, base[iz].Rank)
	}

	// EQUAL commit_count → the commit boost is identical and cancels. Only
	// author_count differs, so any order change is the author term's doing.
	if err := st.UpsertFileHistory(FileHistory{Path: "aaaaa.md", CommitCount: 10, AuthorCount: 1, LastCommitAt: 1}); err != nil {
		t.Fatalf("UpsertFileHistory aaaaa: %v", err)
	}
	if err := st.UpsertFileHistory(FileHistory{Path: "zzzzz.md", CommitCount: 10, AuthorCount: 20, LastCommitAt: 1}); err != nil {
		t.Fatalf("UpsertFileHistory zzzzz: %v", err)
	}
	treat, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("treatment Search: %v", err)
	}
	ja, jz := indexOfDoc(treat, "aaaaa.md"), indexOfDoc(treat, "zzzzz.md")
	if ja < 0 || jz < 0 {
		t.Fatalf("treatment: both docs must be present, got a=%d z=%d", ja, jz)
	}
	if jz >= ja {
		t.Fatalf("author-count term is inert: equal commit_count but author_count 20 vs 1 did NOT flip "+
			"zzzzz.md above aaaaa.md (a=#%d, z=#%d). Half the churn proxy contributes nothing.", ja, jz)
	}
	t.Logf("author-count term verified load-bearing: author 20 vs 1 (equal commits) flipped zzzzz.md to #%d", jz)
}

// TestHistoryRerankIgnoresMalformedCounts is the defense-in-depth guard against a
// malformed/corrupt file_history row. A real git row always has commit_count≥1 and
// author_count≥1, but the schema does not CHECK >=0, so a hand-corrupted row could
// carry negative counts. Feeding math.Log1p a value < -1 returns NaN, which would
// poison c.Score and leave the search sort comparator (Rank < Rank) undefined —
// corrupting result order. applyHistoryReranking must drop such a row entirely.
//
// Isolation mirrors TestHistoryRerankActivatesOnChurn: aaaaa.md and zzzzz.md are
// identical in every text field and equal-length (8 chars), so with no usable
// history they TIE and FilePath-ascending puts aaaaa.md first. We then write a
// NEGATIVE-count history row on ONE twin. Asserting (a) neither Rank is NaN and
// (b) the twins still tie in FilePath order proves the malformed row produced
// neither a NaN nor a phantom boost — it was ignored, exactly like the baseline.
func TestHistoryRerankIgnoresMalformedCounts(t *testing.T) {
	st := tempStore(t)

	const body = "telemetry metrics overview dashboard"
	twins := []Node{
		{ID: "aaaaa.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "aaaaa.md",
			FilePath: "aaaaa.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
		{ID: "zzzzz.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "zzzzz.md",
			FilePath: "zzzzz.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
	}
	if err := st.InsertNodes(twins); err != nil {
		t.Fatalf("InsertNodes twins: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("aaaaa.md", "aaaaa.md", "h1", "doc", "", body, 1, 5),
		sectionChunk("zzzzz.md", "zzzzz.md", "h1", "doc", "", body, 1, 5),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	// Corrupt ONE twin with negative counts. The schema does not constrain >=0 and
	// neither UpsertFileHistory nor GetFileHistory clamps, so these negatives reach
	// applyHistoryReranking — the guard there is the sole thing preventing a NaN.
	if err := st.UpsertFileHistory(FileHistory{
		Path: "zzzzz.md", CommitCount: -5, AuthorCount: -3, LastCommitAt: 1,
	}); err != nil {
		t.Fatalf("UpsertFileHistory malformed: %v", err)
	}

	results, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	ia, iz := indexOfDoc(results, "aaaaa.md"), indexOfDoc(results, "zzzzz.md")
	if ia < 0 || iz < 0 {
		t.Fatalf("both docs must be present, got a=%d z=%d", ia, iz)
	}

	// Core guard proof: a NaN Rank would mean math.Log1p saw the negative count.
	if math.IsNaN(results[ia].Rank) || math.IsNaN(results[iz].Rank) {
		t.Fatalf("malformed history produced a NaN Rank: aaaaa=%v zzzzz=%v — the negative "+
			"count reached math.Log1p and poisoned the score, corrupting sort order",
			results[ia].Rank, results[iz].Rank)
	}

	// The malformed row must contribute zero, so the twins behave exactly like the
	// no-history baseline: equal Rank, FilePath-ascending order (aaaaa.md first).
	if results[ia].Rank != results[iz].Rank {
		t.Fatalf("malformed history must contribute zero (no phantom boost), but twins diverged: "+
			"aaaaa=%.6f zzzzz=%.6f", results[ia].Rank, results[iz].Rank)
	}
	if ia >= iz {
		t.Fatalf("expected aaaaa.md before zzzzz.md on the ignored-malformed tie (a=%d, z=%d)", ia, iz)
	}
	t.Logf("malformed-row guard held: negative counts ignored, twins tied (a=#%d z=#%d), no NaN", ia, iz)
}
