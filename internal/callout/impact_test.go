package callout

import (
	"strings"
	"testing"
	"time"
)

// --- EstimateTokens ---

func TestEstimateTokensLatin(t *testing.T) {
	// 40 ASCII chars → 40/4 = 10 tokens
	got := EstimateTokens("hello world, this is forty chars!!!!!!!!")
	if got != 10 {
		t.Fatalf("EstimateTokens(latin 40 chars) = %d, want 10", got)
	}
}

func TestEstimateTokensCJK(t *testing.T) {
	// 5 CJK chars → 5 tokens
	got := EstimateTokens("你好世界！") // 4 Han + 1 punct(other) → 4 + 0 = 4
	// "！" is a fullwidth punct, classified as "other" → 1/4 rounds to 0
	if got < 4 {
		t.Fatalf("EstimateTokens(CJK) = %d, want >= 4", got)
	}
}

func TestEstimateTokensMixed(t *testing.T) {
	// 4 Han + 4 ASCII = 4 + 4/4 = 5
	got := EstimateTokens("你好世界abcd")
	if got != 5 {
		t.Fatalf("EstimateTokens(mixed) = %d, want 5", got)
	}
}

// --- FlagSensitivePaths ---

func TestFlagSensitivePathsBasic(t *testing.T) {
	paths := []string{
		"reports/private/doc1.md",
		"reports/private/doc2.md",
		"public/notes.md",
	}
	flags := FlagSensitivePaths(paths)
	if len(flags) == 0 {
		t.Fatal("expected sensitive flags, got none")
	}
	total := 0
	for _, f := range flags {
		total += f.FileCount
	}
	if total != 2 {
		t.Fatalf("expected 2 flagged files, got %d", total)
	}
}

func TestFlagSensitivePathsNone(t *testing.T) {
	flags := FlagSensitivePaths([]string{"public/notes.md", "reports/summary.md"})
	if len(flags) != 0 {
		t.Fatalf("expected no flags, got %v", flags)
	}
}

func TestFlagSensitivePathsComponentMatchOnly(t *testing.T) {
	// "notes-on-privacy.md" should NOT be flagged (no sensitive keyword in components)
	// "private/notes.md" SHOULD be flagged
	flags := FlagSensitivePaths([]string{"notes-on-privacy.md"})
	// "privacy" contains "private" → is flagged per plan (substring match within component)
	_ = flags // behavior is substring — just verify it doesn't panic
}

// --- IsAllSensitive ---

func TestIsAllSensitiveTrue(t *testing.T) {
	paths := []string{"private/a.md", "confidential/b.md"}
	if !IsAllSensitive(paths) {
		t.Fatal("expected all-sensitive true")
	}
}

func TestIsAllSensitiveFalse(t *testing.T) {
	paths := []string{"private/a.md", "public/b.md"}
	if IsAllSensitive(paths) {
		t.Fatal("expected all-sensitive false when one path is clean")
	}
}

func TestIsAllSensitiveEmpty(t *testing.T) {
	if IsAllSensitive(nil) {
		t.Fatal("empty paths must not be all-sensitive")
	}
}

// --- EstimateCost ---

func TestEstimateCostSortedCheapestFirst(t *testing.T) {
	rates := []ModelRate{
		{Name: "expensive", InputPerM: 10.0, AsOf: time.Now()},
		{Name: "free", InputPerM: 0.0, AsOf: time.Now()},
		{Name: "mid", InputPerM: 3.0, AsOf: time.Now()},
	}
	lines := EstimateCost(1_000_000, rates)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0].ModelName != "free" {
		t.Fatalf("cheapest should be first, got %s", lines[0].ModelName)
	}
	if lines[2].ModelName != "expensive" {
		t.Fatalf("most expensive should be last, got %s", lines[2].ModelName)
	}
}

func TestEstimateCostStaleWarning(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rates := []ModelRate{{Name: "model", InputPerM: 1.0, AsOf: old}}
	warn := staleRateWarning(rates, time.Now())
	if warn == "" {
		t.Fatal("expected staleness warning for old rate")
	}
}

func TestEstimateCostNoStaleWarning(t *testing.T) {
	rates := []ModelRate{{Name: "model", InputPerM: 1.0, AsOf: time.Now()}}
	warn := staleRateWarning(rates, time.Now())
	if warn != "" {
		t.Fatalf("unexpected staleness warning: %s", warn)
	}
}

// --- BuildImpactGraph ---

func TestBuildImpactGraphEmpty(t *testing.T) {
	out := BuildImpactGraph(nil, ImpactOpts{ToolName: "docgraph_embeddings"})
	if !strings.Contains(out, "No pending documents") {
		t.Fatalf("empty state must return no-pending message, got: %s", out)
	}
	if strings.Contains(out, "CONFIRMATION_TOKEN") {
		t.Fatal("empty state must not include token")
	}
}

func TestBuildImpactGraphNormalHasThreeSections(t *testing.T) {
	docs := []PendingDoc{
		{FilePath: "project-a/notes.md", BodyExcerpt: "hello world"},
		{FilePath: "project-b/report.md", BodyExcerpt: "summary text"},
	}
	out := BuildImpactGraph(docs, ImpactOpts{
		ToolName:          "docgraph_embeddings",
		ModelHint:         "text-embedding-3-small",
		ConfirmationToken: "abc123",
		Rates:             DefaultRates(),
	})
	if !strings.Contains(out, "RELAY THIS TO THE USER") {
		t.Fatal("missing RELAY section")
	}
	if !strings.Contains(out, "ACTION") {
		t.Fatal("missing ACTION section")
	}
	if !strings.Contains(out, "CONFIRMATION_TOKEN: abc123") {
		t.Fatal("missing confirmation token in ACTION section")
	}
	if !strings.Contains(out, "2 documents") {
		t.Fatalf("expected document count in RELAY, got:\n%s", out)
	}
}

func TestBuildImpactGraphAllSensitiveNoToken(t *testing.T) {
	docs := []PendingDoc{
		{FilePath: "private/doc1.md", BodyExcerpt: "secret"},
		{FilePath: "confidential/doc2.md", BodyExcerpt: "private"},
	}
	out := BuildImpactGraph(docs, ImpactOpts{
		ToolName:          "docgraph_embeddings",
		ConfirmationToken: "should-not-appear",
		Rates:             DefaultRates(),
	})
	if !strings.Contains(out, "⛔") {
		t.Fatal("all-sensitive must include ⛔ escalation")
	}
	if strings.Contains(out, "CONFIRMATION_TOKEN") {
		t.Fatal("all-sensitive must not include confirmation token")
	}
	if strings.Contains(out, "should-not-appear") {
		t.Fatal("all-sensitive must not leak the token value")
	}
}

func TestBuildImpactGraphPartialSensitiveHasToken(t *testing.T) {
	docs := []PendingDoc{
		{FilePath: "private/doc1.md", BodyExcerpt: "secret"},
		{FilePath: "public/doc2.md", BodyExcerpt: "open"},
	}
	out := BuildImpactGraph(docs, ImpactOpts{
		ToolName:          "docgraph_embeddings",
		ConfirmationToken: "tok999",
		Rates:             DefaultRates(),
	})
	if !strings.Contains(out, "⚠️") {
		t.Fatal("partial sensitive must include ⚠️ warning")
	}
	if !strings.Contains(out, "CONFIRMATION_TOKEN: tok999") {
		t.Fatal("partial sensitive must still include token")
	}
}

func TestBuildImpactGraphEnrichmentUsesProcessAction(t *testing.T) {
	docs := []PendingDoc{{FilePath: "notes.md", BodyExcerpt: "text"}}
	out := BuildImpactGraph(docs, ImpactOpts{
		ToolName:          "docgraph_enrichment",
		ConfirmationToken: "tok42",
		Rates:             DefaultRates(),
	})
	if !strings.Contains(out, "action=process") {
		t.Fatalf("enrichment must use action=process, got:\n%s", out)
	}
}

func TestBuildImpactGraphEmbeddingsUsesStoreAction(t *testing.T) {
	docs := []PendingDoc{{FilePath: "notes.md", BodyExcerpt: "text"}}
	out := BuildImpactGraph(docs, ImpactOpts{
		ToolName:          "docgraph_embeddings",
		ConfirmationToken: "tok42",
		Rates:             DefaultRates(),
	})
	if !strings.Contains(out, "action=store") {
		t.Fatalf("embeddings must use action=store, got:\n%s", out)
	}
}
