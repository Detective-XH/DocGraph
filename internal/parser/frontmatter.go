package parser

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// ExtractFrontmatter extracts YAML frontmatter from markdown bytes using goldmark-meta.
// Returns nil, nil if no frontmatter is present.
func ExtractFrontmatter(source []byte) (map[string]interface{}, error) {
	md := goldmark.New(goldmark.WithExtensions(MetaExt))
	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(source), parser.WithContext(ctx))
	_ = doc

	fm := MetaGet(ctx)
	if len(fm) == 0 {
		return nil, nil
	}
	return fm, nil
}

// FrontmatterToJSON converts a frontmatter map to a JSON string.
// Returns "" if fm is nil.
func FrontmatterToJSON(fm map[string]interface{}) string {
	if fm == nil {
		return ""
	}
	b, err := json.Marshal(fm)
	if err != nil {
		return ""
	}
	return string(b)
}

// GetTags extracts the "tags" field from frontmatter, handling both []interface{} and []string.
func GetTags(fm map[string]interface{}) []string {
	if fm == nil {
		return nil
	}
	raw, ok := fm["tags"]
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case []interface{}:
		tags := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return v
	default:
		return nil
	}
}

// GetTitle extracts the "title" field from frontmatter.
func GetTitle(fm map[string]interface{}) string {
	if fm == nil {
		return ""
	}
	v, ok := fm["title"]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// GetWikilinks scans all string values in the frontmatter map for [[...]] patterns
// and returns the wikilink targets. Handles both string and slice values.
func GetWikilinks(fm map[string]interface{}) []string {
	if fm == nil {
		return nil
	}
	var targets []string
	for _, val := range fm {
		switch v := val.(type) {
		case string:
			targets = append(targets, extractWikilinks(v)...)
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					targets = append(targets, extractWikilinks(s)...)
				}
			}
		case []string:
			for _, s := range v {
				targets = append(targets, extractWikilinks(s)...)
			}
		}
	}
	return targets
}

func extractWikilinks(s string) []string {
	matches := wikilinkRe.FindAllStringSubmatch(s, -1)
	targets := make([]string, 0, len(matches))
	for _, m := range matches {
		targets = append(targets, m[1])
	}
	return targets
}

// bodyAfterFrontmatter returns the source bytes after the YAML frontmatter block.
// If no frontmatter is found, returns the original source.
func bodyAfterFrontmatter(source []byte) []byte {
	if !bytes.HasPrefix(source, []byte("---")) {
		return source
	}
	// Find the closing ---
	rest := source[3:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return source
	}
	after := rest[idx+4:] // skip past "\n---"
	// Skip optional trailing newline
	if len(after) > 0 && after[0] == '\n' {
		after = after[1:]
	}
	return after
}
