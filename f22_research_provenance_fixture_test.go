package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/parser"
)

func TestF22ResearchProvenanceFixtures(t *testing.T) {
	// F-22 parallelization guardrail: these fixtures define the frontmatter
	// shapes that research provenance indexing must consume after F-21 lands.
	// The test intentionally verifies raw frontmatter only; it does not depend
	// on schema migrations, normalized metadata tables, or typed research indexes.
	dir := filepath.Join("testdata", "research-provenance")

	tests := []struct {
		file         string
		requiredKeys []string
	}{
		{
			file: "valid-claim.md",
			requiredKeys: []string{
				"claim_id",
				"evidence",
				"source_type",
				"confidence",
				"event_date",
				"assessment_date",
				"last_verified",
				"valid_until",
				"analyst_status",
				"client",
				"deliverable_id",
			},
		},
		{
			file:         "minimal-claim.md",
			requiredKeys: []string{"claim_id", "confidence"},
		},
		{
			file:         "list-evidence.md",
			requiredKeys: []string{"claim_id", "evidence", "source_type", "confidence", "analyst_status"},
		},
		{
			file:         "advisory-conflict.md",
			requiredKeys: []string{"claim_id", "confidence", "source_type", "skill_advisory"},
		},
		{
			file:         "invalid-values-preserved.md",
			requiredKeys: []string{"claim_id", "confidence", "source_type", "analyst_status", "event_date"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}

			fm, err := parser.ExtractFrontmatter(source)
			if err != nil {
				t.Fatalf("extract frontmatter: %v", err)
			}
			if fm == nil {
				t.Fatal("expected research provenance frontmatter")
			}

			for _, key := range tc.requiredKeys {
				if _, ok := fm[key]; !ok {
					t.Fatalf("expected key %q in %s", key, tc.file)
				}
			}
		})
	}
}

func TestF22ResearchProvenanceFixtureShapes(t *testing.T) {
	dir := filepath.Join("testdata", "research-provenance")

	valid := mustFixtureFrontmatter(t, dir, "valid-claim.md")
	evidence, ok := valid["evidence"].([]interface{})
	if !ok {
		t.Fatalf("valid-claim evidence has type %T, want []interface{}", valid["evidence"])
	}
	if len(evidence) != 2 {
		t.Fatalf("valid-claim evidence length = %d, want 2", len(evidence))
	}

	advisory := mustFixtureFrontmatter(t, dir, "advisory-conflict.md")
	if _, ok := advisory["skill_advisory"].(map[string]interface{}); !ok {
		t.Fatalf("skill_advisory has type %T, want map[string]interface{}", advisory["skill_advisory"])
	}

	invalid := mustFixtureFrontmatter(t, dir, "invalid-values-preserved.md")
	if got := invalid["confidence"]; got != "extremely-certain" {
		t.Fatalf("invalid confidence = %v, want preserved value", got)
	}
	if got := invalid["event_date"]; got != "not-a-date" {
		t.Fatalf("invalid event_date = %v, want preserved value", got)
	}
}

func mustFixtureFrontmatter(t *testing.T, dir, file string) map[string]interface{} {
	t.Helper()

	source, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	fm, err := parser.ExtractFrontmatter(source)
	if err != nil {
		t.Fatalf("extract frontmatter: %v", err)
	}
	if fm == nil {
		t.Fatal("expected frontmatter")
	}
	return fm
}
