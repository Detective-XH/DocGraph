package parser

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Detective-XH/docgraph/internal/store"
)

const (
	maxTuplesPerDoc = 200
	maxValueLen     = 2000
)

// iso8601Re matches ISO 8601 date strings: YYYY-MM-DD optionally with time/timezone.
var iso8601Re = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T[\d:.Z+-]*)?$`)

// wikilinkValueRe matches [[...]] wikilink patterns used as values.
var wikilinkValueRe = regexp.MustCompile(`^\[\[.+\]\]$`)

// ExtractMetadataTuples converts a frontmatter map into normalized MetadataTuples.
// Rules:
//   - "tags" key is skipped (already handled by tag nodes + tagged edges).
//   - Output is capped at maxTuplesPerDoc; excess keys are logged and dropped.
//   - Values are truncated to maxValueLen characters.
//   - value_type is inferred: date, bool, list (JSON array), number, ref, or string.
//   - Top-level tuples use source="frontmatter".
//   - Nested "skill_advisory" values use source="skill_advisory".
func ExtractMetadataTuples(fm map[string]interface{}) []store.MetadataTuple {
	if len(fm) == 0 {
		return nil
	}

	out := make([]store.MetadataTuple, 0, len(fm))
	now := time.Now().Unix()

	for k, v := range fm {
		if k == "tags" {
			continue
		}
		if k == "skill_advisory" {
			out = append(out, flattenAdvisoryValue(v, now)...)
			continue
		}
		tuples := flattenValue(k, v, now)
		out = append(out, tuples...)
	}

	if len(out) > maxTuplesPerDoc {
		log.Printf("ExtractMetadataTuples: doc has %d tuples, truncating to %d", len(out), maxTuplesPerDoc)
		out = out[:maxTuplesPerDoc]
	}
	return out
}

func flattenAdvisoryValue(val interface{}, updatedAt int64) []store.MetadataTuple {
	m, ok := val.(map[string]interface{})
	if !ok {
		return nil
	}
	var out []store.MetadataTuple
	for k, v := range m {
		tuples := flattenValue(k, v, updatedAt)
		for i := range tuples {
			tuples[i].Source = "skill_advisory"
		}
		out = append(out, tuples...)
	}
	return out
}

// flattenValue converts a single frontmatter key/value into one or more MetadataTuples.
func flattenValue(key string, val interface{}, updatedAt int64) []store.MetadataTuple {
	switch v := val.(type) {
	case []interface{}:
		return []store.MetadataTuple{listTuple(key, v, updatedAt)}
	case []string:
		items := make([]interface{}, len(v))
		for i, s := range v {
			items[i] = s
		}
		return []store.MetadataTuple{listTuple(key, items, updatedAt)}
	case bool:
		return []store.MetadataTuple{{
			Key:       key,
			Value:     fmt.Sprintf("%t", v),
			ValueType: "bool",
			Source:    "frontmatter",
		}}
	case int, int64, float64:
		return []store.MetadataTuple{{
			Key:       key,
			Value:     truncateStr(fmt.Sprintf("%v", v), maxValueLen),
			ValueType: "number",
			Source:    "frontmatter",
		}}
	case map[string]interface{}:
		// Nested object: encode as JSON string, treat as string type.
		b, _ := json.Marshal(v)
		return []store.MetadataTuple{{
			Key:       key,
			Value:     truncateStr(string(b), maxValueLen),
			ValueType: "string",
			Source:    "frontmatter",
		}}
	case string:
		return []store.MetadataTuple{stringTuple(key, v, updatedAt)}
	default:
		if v == nil {
			return nil
		}
		return []store.MetadataTuple{{
			Key:       key,
			Value:     truncateStr(fmt.Sprintf("%v", v), maxValueLen),
			ValueType: "string",
			Source:    "frontmatter",
		}}
	}
}

func stringTuple(key, v string, _ int64) store.MetadataTuple {
	value := truncateStr(v, maxValueLen)
	vtype := inferStringType(value)
	return store.MetadataTuple{
		Key:       key,
		Value:     value,
		ValueType: vtype,
		Source:    "frontmatter",
	}
}

func listTuple(key string, items []interface{}, _ int64) store.MetadataTuple {
	strs := make([]string, 0, len(items))
	for _, item := range items {
		strs = append(strs, fmt.Sprintf("%v", item))
	}
	b, _ := json.Marshal(strs)
	value := truncateStr(string(b), maxValueLen)
	return store.MetadataTuple{
		Key:       key,
		Value:     value,
		ValueType: "list",
		Source:    "frontmatter",
	}
}

// inferStringType returns the value_type for a string value.
func inferStringType(s string) string {
	s = strings.TrimSpace(s)
	if iso8601Re.MatchString(s) {
		// Validate it's a plausible date (not just any YYYY-MM-DD-like number).
		if _, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return "date"
		}
	}
	if wikilinkValueRe.MatchString(s) {
		return "ref"
	}
	// Check if string is all digits / numeric (int or float).
	if isNumericString(s) {
		return "number"
	}
	return "string"
}

// isNumericString returns true if s represents a numeric value.
func isNumericString(s string) bool {
	if s == "" {
		return false
	}
	dots := 0
	for i, ch := range s {
		if ch == '-' && i == 0 {
			continue
		}
		if ch == '.' {
			dots++
			if dots > 1 {
				return false
			}
			continue
		}
		if !unicode.IsDigit(ch) {
			return false
		}
	}
	return true
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
