package parser

import (
	"fmt"
	"strings"
	"testing"
)

// TestExtractMetadataTuples_Nil verifies that a nil map returns nil without panicking.
func TestExtractMetadataTuples_Nil(t *testing.T) {
	result := ExtractMetadataTuples(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

// TestExtractMetadataTuples_Empty verifies that an empty map returns nil or empty.
func TestExtractMetadataTuples_Empty(t *testing.T) {
	result := ExtractMetadataTuples(map[string]any{})
	if len(result) != 0 {
		t.Errorf("expected empty result for empty map, got %d tuples", len(result))
	}
}

// TestExtractMetadataTuples_TagsSkipped verifies that the "tags" key is always skipped.
func TestExtractMetadataTuples_TagsSkipped(t *testing.T) {
	fm := map[string]any{
		"tags": []any{"alpha", "beta"},
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 0 {
		t.Errorf("expected empty result when only 'tags' key present, got %d tuples", len(result))
	}
}

// TestExtractMetadataTuples_GovernanceKeys verifies string governance keys are extracted correctly.
func TestExtractMetadataTuples_GovernanceKeys(t *testing.T) {
	fm := map[string]any{
		"status":      "approved",
		"owner":       "Alice",
		"sensitivity": "internal",
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 3 {
		t.Fatalf("expected 3 tuples, got %d", len(result))
	}

	byKey := make(map[string]string)
	for _, tup := range result {
		byKey[tup.Key] = tup.Value
		if tup.ValueType != "string" {
			t.Errorf("key %q: expected ValueType=string, got %q", tup.Key, tup.ValueType)
		}
		if tup.Source != "frontmatter" {
			t.Errorf("key %q: expected Source=frontmatter, got %q", tup.Key, tup.Source)
		}
	}

	want := map[string]string{
		"status":      "approved",
		"owner":       "Alice",
		"sensitivity": "internal",
	}
	for k, v := range want {
		if byKey[k] != v {
			t.Errorf("key %q: expected value %q, got %q", k, v, byKey[k])
		}
	}
}

// TestExtractMetadataTuples_DateInference verifies ISO 8601 date strings get ValueType="date".
func TestExtractMetadataTuples_DateInference(t *testing.T) {
	fm := map[string]any{
		"effective_date": "2026-01-15",
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if result[0].ValueType != "date" {
		t.Errorf("expected ValueType=date for ISO date string, got %q", result[0].ValueType)
	}
	if result[0].Value != "2026-01-15" {
		t.Errorf("expected Value=2026-01-15, got %q", result[0].Value)
	}
}

// TestExtractMetadataTuples_BoolInference verifies bool values get ValueType="bool".
func TestExtractMetadataTuples_BoolInference(t *testing.T) {
	fm := map[string]any{
		"some_flag": true,
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if result[0].ValueType != "bool" {
		t.Errorf("expected ValueType=bool, got %q", result[0].ValueType)
	}
	if result[0].Value != "true" {
		t.Errorf("expected Value=true, got %q", result[0].Value)
	}
}

// TestExtractMetadataTuples_ListInference verifies []interface{} slices get ValueType="list"
// and are encoded as a JSON array.
func TestExtractMetadataTuples_ListInference(t *testing.T) {
	fm := map[string]any{
		"related": []any{"a", "b"},
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if result[0].ValueType != "list" {
		t.Errorf("expected ValueType=list, got %q", result[0].ValueType)
	}
	if result[0].Value != `["a","b"]` {
		t.Errorf("expected Value=[\"a\",\"b\"], got %q", result[0].Value)
	}
}

// TestExtractMetadataTuples_WikilinkRef verifies [[...]] values get ValueType="ref".
func TestExtractMetadataTuples_WikilinkRef(t *testing.T) {
	fm := map[string]any{
		"supersedes": "[[old-policy]]",
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if result[0].ValueType != "ref" {
		t.Errorf("expected ValueType=ref for wikilink value, got %q", result[0].ValueType)
	}
	if result[0].Value != "[[old-policy]]" {
		t.Errorf("expected Value=[[old-policy]], got %q", result[0].Value)
	}
}

// TestExtractMetadataTuples_NumberInference verifies int values get ValueType="number".
func TestExtractMetadataTuples_NumberInference(t *testing.T) {
	fm := map[string]any{
		"revision": int(3),
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if result[0].ValueType != "number" {
		t.Errorf("expected ValueType=number for int value, got %q", result[0].ValueType)
	}
	if result[0].Value != "3" {
		t.Errorf("expected Value=3, got %q", result[0].Value)
	}
}

// TestExtractMetadataTuples_ValueTruncation verifies values longer than 2000 chars are truncated.
func TestExtractMetadataTuples_ValueTruncation(t *testing.T) {
	longVal := strings.Repeat("x", 3000)
	fm := map[string]any{
		"description": longVal,
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if len(result[0].Value) != 2000 {
		t.Errorf("expected value truncated to 2000 chars, got %d chars", len(result[0].Value))
	}
}

// TestExtractMetadataTuples_TupleCap verifies that output is capped at 200 tuples.
func TestExtractMetadataTuples_TupleCap(t *testing.T) {
	fm := make(map[string]any, 250)
	for i := 0; i < 250; i++ {
		fm[fmt.Sprintf("key_%d", i)] = fmt.Sprintf("value_%d", i)
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 200 {
		t.Errorf("expected exactly 200 tuples (cap), got %d", len(result))
	}
}

// TestExtractMetadataTuples_InvalidDateNotInferred verifies that an invalid date string
// does not get ValueType="date" — it should remain "string".
func TestExtractMetadataTuples_InvalidDateNotInferred(t *testing.T) {
	fm := map[string]any{
		"bad_date": "2026-13-99",
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 1 {
		t.Fatalf("expected 1 tuple, got %d", len(result))
	}
	if result[0].ValueType != "string" {
		t.Errorf("expected ValueType=string for invalid date, got %q", result[0].ValueType)
	}
}

// TestExtractMetadataTuples_AllSourceFrontmatter verifies that all returned tuples
// have Source="frontmatter".
func TestExtractMetadataTuples_AllSourceFrontmatter(t *testing.T) {
	fm := map[string]any{
		"status":         "draft",
		"effective_date": "2026-06-01",
		"count":          int(5),
		"active":         false,
		"items":          []any{"x", "y"},
		"ref_doc":        "[[some-doc]]",
	}
	result := ExtractMetadataTuples(fm)
	for _, tup := range result {
		if tup.Source != "frontmatter" {
			t.Errorf("key %q: expected Source=frontmatter, got %q", tup.Key, tup.Source)
		}
	}
}

func TestExtractMetadataTuples_SkillAdvisorySource(t *testing.T) {
	fm := map[string]any{
		"status": "approved",
		"skill_advisory": map[string]any{
			"status":      "draft",
			"sensitivity": "restricted",
		},
	}

	result := ExtractMetadataTuples(fm)
	if len(result) != 3 {
		t.Fatalf("expected 3 tuples, got %d", len(result))
	}

	values := map[string]map[string]string{}
	for _, tup := range result {
		if values[tup.Key] == nil {
			values[tup.Key] = map[string]string{}
		}
		values[tup.Key][tup.Source] = tup.Value
	}

	if values["status"]["frontmatter"] != "approved" {
		t.Errorf("expected frontmatter status=approved, got %q", values["status"]["frontmatter"])
	}
	if values["status"]["skill_advisory"] != "draft" {
		t.Errorf("expected advisory status=draft, got %q", values["status"]["skill_advisory"])
	}
	if values["sensitivity"]["skill_advisory"] != "restricted" {
		t.Errorf("expected advisory sensitivity=restricted, got %q", values["sensitivity"]["skill_advisory"])
	}
}

// TestExtractMetadataTuples_MixedTypes verifies correct ValueType inference across
// string, bool, int, list, date, and ref values in a single map.
func TestExtractMetadataTuples_MixedTypes(t *testing.T) {
	fm := map[string]any{
		"label":          "some text",
		"active":         true,
		"count":          int(42),
		"items":          []any{"p", "q"},
		"effective_date": "2025-03-10",
		"ref_key":        "[[target-doc]]",
	}
	result := ExtractMetadataTuples(fm)
	if len(result) != 6 {
		t.Fatalf("expected 6 tuples, got %d", len(result))
	}

	byKey := make(map[string]string)
	for _, tup := range result {
		byKey[tup.Key] = tup.ValueType
	}

	cases := map[string]string{
		"label":          "string",
		"active":         "bool",
		"count":          "number",
		"items":          "list",
		"effective_date": "date",
		"ref_key":        "ref",
	}
	for key, wantType := range cases {
		if got, ok := byKey[key]; !ok {
			t.Errorf("key %q not found in result", key)
		} else if got != wantType {
			t.Errorf("key %q: expected ValueType=%q, got %q", key, wantType, got)
		}
	}
}
