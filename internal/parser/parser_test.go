package parser

import (
	"testing"
)

func parseTestSource(t *testing.T, source string) *ParseResult {
	t.Helper()
	res, err := ParseFile("/test/file.md", "file.md", []byte(source), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestParseFileBasic(t *testing.T) {
	source := `# My Document

Some intro text.

## Section One

Content for section one.

## Section Two

Content for section two.
`
	res := parseTestSource(t, source)

	t.Run("document node", func(t *testing.T) {
		if res.DocNode.Kind != "document" {
			t.Errorf("expected kind=document, got %q", res.DocNode.Kind)
		}
		if res.DocNode.Name != "My Document" {
			t.Errorf("expected name='My Document', got %q", res.DocNode.Name)
		}
		if res.DocNode.ID != "file.md" {
			t.Errorf("expected ID='file.md', got %q", res.DocNode.ID)
		}
	})

	t.Run("heading count", func(t *testing.T) {
		// H1 + 2 H2 = 3 headings total
		if len(res.Headings) != 3 {
			t.Fatalf("expected 3 headings, got %d", len(res.Headings))
		}
	})

	t.Run("heading names", func(t *testing.T) {
		names := make([]string, len(res.Headings))
		for i, h := range res.Headings {
			names[i] = h.Name
		}
		expected := []string{"My Document", "Section One", "Section Two"}
		for i, want := range expected {
			if names[i] != want {
				t.Errorf("heading[%d]: expected %q, got %q", i, want, names[i])
			}
		}
	})

	t.Run("containment edges", func(t *testing.T) {
		// Doc → H1, H1 → H2(Section One), H1 → H2(Section Two)
		containsCount := 0
		for _, e := range res.Edges {
			if e.Kind == "contains" {
				containsCount++
			}
		}
		if containsCount != 3 {
			t.Errorf("expected 3 containment edges, got %d", containsCount)
		}
	})
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ASCII", "Hello World", "hello-world"},
		{"CJK", "第一章 介紹", "第一章-介紹"},
		{"special chars", "C++ & Go!", "c-go"},
		{"empty", "", ""},
		{"dashes preserved", "well-known", "well-known"},
		{"multiple spaces", "a   b", "a-b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.in)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSlugCollision(t *testing.T) {
	source := `# Title

## Examples

First examples section.

## Examples

Second examples section.
`
	res := parseTestSource(t, source)

	// Find heading IDs for "Examples"
	var exampleIDs []string
	for _, h := range res.Headings {
		if h.Name == "Examples" {
			exampleIDs = append(exampleIDs, h.ID)
		}
	}

	if len(exampleIDs) != 2 {
		t.Fatalf("expected 2 headings named 'Examples', got %d", len(exampleIDs))
	}

	if exampleIDs[0] != "file.md#examples" {
		t.Errorf("first Examples ID: expected 'file.md#examples', got %q", exampleIDs[0])
	}
	if exampleIDs[1] != "file.md#examples-2" {
		t.Errorf("second Examples ID: expected 'file.md#examples-2', got %q", exampleIDs[1])
	}
}

func TestFrontmatterExtraction(t *testing.T) {
	source := `---
title: "My Report"
tags:
  - analysis
  - osint
related_to: "See [[OtherDoc]] for details"
---

# My Report

Some content here.
`
	res := parseTestSource(t, source)

	t.Run("file has frontmatter", func(t *testing.T) {
		if !res.FileInfo.HasFrontmatter {
			t.Error("expected HasFrontmatter=true")
		}
	})

	// Extract frontmatter directly to test helper functions.
	fm, err := ExtractFrontmatter([]byte(source))
	if err != nil {
		t.Fatalf("ExtractFrontmatter failed: %v", err)
	}

	t.Run("GetTitle", func(t *testing.T) {
		title := GetTitle(fm)
		if title != "My Report" {
			t.Errorf("expected title='My Report', got %q", title)
		}
	})

	t.Run("GetTags", func(t *testing.T) {
		tags := GetTags(fm)
		if len(tags) != 2 {
			t.Fatalf("expected 2 tags, got %d", len(tags))
		}
		if tags[0] != "analysis" {
			t.Errorf("expected tags[0]='analysis', got %q", tags[0])
		}
		if tags[1] != "osint" {
			t.Errorf("expected tags[1]='osint', got %q", tags[1])
		}
	})

	t.Run("GetWikilinks", func(t *testing.T) {
		wikilinks := GetWikilinks(fm)
		if len(wikilinks) != 1 {
			t.Fatalf("expected 1 wikilink, got %d", len(wikilinks))
		}
		if wikilinks[0] != "OtherDoc" {
			t.Errorf("expected wikilink target='OtherDoc', got %q", wikilinks[0])
		}
	})

	t.Run("wikilink appears in RawLinks", func(t *testing.T) {
		found := false
		for _, rl := range res.RawLinks {
			if rl.Target == "OtherDoc" && rl.Kind == "wikilink" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected frontmatter wikilink 'OtherDoc' in RawLinks")
		}
	})
}

func TestWikilinkDetection(t *testing.T) {
	// Goldmark splits [[...]] across multiple ast.Text nodes, so inline
	// wikilinks are NOT detected by the regex-on-Text-segment approach.
	// Wikilinks are reliably detected only from frontmatter values.
	// This test verifies frontmatter wikilink and embed extraction via
	// the RawLinks pipeline.
	source := `---
related_to: "[[target]]"
see_also:
  - "[[aliased]]"
  - "![[embed]]"
---

# Links

Some body text.
`
	res := parseTestSource(t, source)

	t.Run("raw link count", func(t *testing.T) {
		// 3 wikilinks extracted from frontmatter values.
		// Note: ![[embed]] in frontmatter is matched by GetWikilinks as "embed"
		// (the ! prefix is outside the [[ ]] pattern in the frontmatter scanner).
		if len(res.RawLinks) != 3 {
			t.Fatalf("expected 3 raw links, got %d", len(res.RawLinks))
		}
	})

	// Build a map of target → kind for easy lookup.
	linkMap := make(map[string]string)
	for _, rl := range res.RawLinks {
		linkMap[rl.Target] = rl.Kind
	}

	t.Run("wikilink from frontmatter", func(t *testing.T) {
		if kind, ok := linkMap["target"]; !ok {
			t.Error("expected link with target='target'")
		} else if kind != "wikilink" {
			t.Errorf("expected kind=wikilink, got %q", kind)
		}
	})

	t.Run("aliased wikilink from frontmatter", func(t *testing.T) {
		if kind, ok := linkMap["aliased"]; !ok {
			t.Error("expected link with target='aliased'")
		} else if kind != "wikilink" {
			t.Errorf("expected kind=wikilink, got %q", kind)
		}
	})
}

func TestInlineWikilinkDetection(t *testing.T) {
	source := `# Title

## Section A

This has a [[target-doc]] wikilink inline.

Also [[another|with alias]] here.

## Section B

An embed: ![[embedded-doc]]
`
	res := parseTestSource(t, source)

	linkMap := make(map[string]string)
	for _, rl := range res.RawLinks {
		linkMap[rl.Target] = rl.Kind
	}

	t.Run("inline wikilink detected", func(t *testing.T) {
		if _, ok := linkMap["target-doc"]; !ok {
			t.Errorf("expected inline wikilink 'target-doc', got links: %v", linkMap)
		}
	})

	t.Run("inline aliased wikilink detected", func(t *testing.T) {
		if _, ok := linkMap["another"]; !ok {
			t.Errorf("expected inline wikilink 'another', got links: %v", linkMap)
		}
	})

	t.Run("inline embed detected", func(t *testing.T) {
		if kind, ok := linkMap["embedded-doc"]; !ok {
			t.Errorf("expected inline embed 'embedded-doc', got links: %v", linkMap)
		} else if kind != "embed" {
			t.Errorf("expected kind=embed, got %q", kind)
		}
	})

	t.Run("wikilink assigned to correct heading", func(t *testing.T) {
		for _, rl := range res.RawLinks {
			if rl.Target == "embedded-doc" {
				if rl.FromNodeID != "file.md#section-b" {
					t.Errorf("expected FromNodeID='file.md#section-b', got %q", rl.FromNodeID)
				}
				break
			}
		}
	})
}

func TestInlineWikilinksIgnoreCodeBlocksAndComments(t *testing.T) {
	source := "# Title\n\n" +
		"This real link should count: [[real-doc]].\n\n" +
		"```markdown\n" +
		"[[fenced-doc]]\n" +
		"```\n\n" +
		"~~~\n" +
		"![[tilde-fenced-doc]]\n" +
		"~~~\n\n" +
		"<!-- [[comment-doc]] -->\n" +
		"Visible [[visible-doc]] <!-- [[inline-comment-doc]] --> still visible [[after-comment-doc]].\n" +
		"<!--\n" +
		"[[multiline-comment-doc]]\n" +
		"-->\n" +
		"Final ![[real-embed]].\n"

	res := parseTestSource(t, source)

	linkMap := make(map[string]string)
	for _, rl := range res.RawLinks {
		linkMap[rl.Target] = rl.Kind
	}

	for _, target := range []string{"real-doc", "visible-doc", "after-comment-doc", "real-embed"} {
		if _, ok := linkMap[target]; !ok {
			t.Errorf("expected visible wikilink %q, got links: %v", target, linkMap)
		}
	}
	for _, target := range []string{"fenced-doc", "tilde-fenced-doc", "comment-doc", "inline-comment-doc", "multiline-comment-doc"} {
		if _, ok := linkMap[target]; ok {
			t.Errorf("did not expect wikilink %q from code/comment content", target)
		}
	}
	if kind := linkMap["real-embed"]; kind != "embed" {
		t.Errorf("expected real-embed kind=embed, got %q", kind)
	}
}

func TestMarkdownLinkDetection(t *testing.T) {
	source := `# Links

A local link [text](path.md) and an external [text](https://url.com).
`
	res := parseTestSource(t, source)

	if len(res.RawLinks) != 2 {
		t.Fatalf("expected 2 raw links, got %d", len(res.RawLinks))
	}

	// Build a map of target → kind.
	linkMap := make(map[string]string)
	for _, rl := range res.RawLinks {
		linkMap[rl.Target] = rl.Kind
	}

	t.Run("markdown link to local file", func(t *testing.T) {
		if kind, ok := linkMap["path.md"]; !ok {
			t.Error("expected link with target='path.md'")
		} else if kind != "markdown_link" {
			t.Errorf("expected kind=markdown_link, got %q", kind)
		}
	})

	t.Run("external link", func(t *testing.T) {
		if kind, ok := linkMap["https://url.com"]; !ok {
			t.Error("expected link with target='https://url.com'")
		} else if kind != "external" {
			t.Errorf("expected kind=external, got %q", kind)
		}
	})
}

func TestDefinitionExtraction(t *testing.T) {
	source := "# Glossary\n\n" +
		"**Alpha:** First definition.\n\n" +
		"```markdown\n" +
		"**Ignored:** Inside code.\n" +
		"```\n\n" +
		"<!-- **Hidden:** Inside comment. -->\n\n" +
		"## Terms\n\n" +
		"**Beta:** Second definition.\n"
	res := parseTestSource(t, source)

	if len(res.Defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(res.Defs))
	}

	defs := make(map[string]int)
	for i, def := range res.Defs {
		defs[def.Name] = i
	}

	alphaIdx, ok := defs["Alpha"]
	if !ok {
		t.Fatalf("expected Alpha definition, got %#v", defs)
	}
	alpha := res.Defs[alphaIdx]
	if alpha.Kind != "definition" {
		t.Errorf("expected Alpha kind=definition, got %q", alpha.Kind)
	}
	if alpha.BodyExcerpt != "First definition." {
		t.Errorf("expected Alpha body excerpt, got %q", alpha.BodyExcerpt)
	}
	if _, ok := defs["Ignored"]; ok {
		t.Error("did not expect definition inside fenced code block")
	}
	if _, ok := defs["Hidden"]; ok {
		t.Error("did not expect definition inside HTML comment")
	}

	beta := res.Defs[defs["Beta"]]
	var betaParent string
	for _, edge := range res.Edges {
		if edge.Target == beta.ID && edge.Kind == "contains" {
			betaParent = edge.Source
			break
		}
	}
	if betaParent != "file.md#terms" {
		t.Errorf("expected Beta parent file.md#terms, got %q", betaParent)
	}
}

func TestEmptyFile(t *testing.T) {
	res := parseTestSource(t, "")

	t.Run("no crash", func(t *testing.T) {
		if res == nil {
			t.Fatal("expected non-nil result for empty file")
		}
	})

	t.Run("document node exists", func(t *testing.T) {
		if res.DocNode.Kind != "document" {
			t.Errorf("expected kind=document, got %q", res.DocNode.Kind)
		}
	})

	t.Run("document name fallback", func(t *testing.T) {
		// No H1, no frontmatter title → falls back to filename without extension.
		if res.DocNode.Name != "file" {
			t.Errorf("expected name='file' (filename fallback), got %q", res.DocNode.Name)
		}
	})

	t.Run("zero headings", func(t *testing.T) {
		if len(res.Headings) != 0 {
			t.Errorf("expected 0 headings, got %d", len(res.Headings))
		}
	})
}

func TestCJKHeadings(t *testing.T) {
	source := `# 情報分析報告

## 第一章 背景

Some content.

## 第二章 結論

Final thoughts.
`
	res := parseTestSource(t, source)

	t.Run("heading count", func(t *testing.T) {
		if len(res.Headings) != 3 {
			t.Fatalf("expected 3 headings, got %d", len(res.Headings))
		}
	})

	t.Run("CJK heading names", func(t *testing.T) {
		expected := []string{"情報分析報告", "第一章 背景", "第二章 結論"}
		for i, want := range expected {
			if res.Headings[i].Name != want {
				t.Errorf("heading[%d]: expected %q, got %q", i, want, res.Headings[i].Name)
			}
		}
	})

	t.Run("CJK slugs", func(t *testing.T) {
		expectedSlugs := []string{
			"file.md#情報分析報告",
			"file.md#第一章-背景",
			"file.md#第二章-結論",
		}
		for i, want := range expectedSlugs {
			if res.Headings[i].ID != want {
				t.Errorf("heading[%d] ID: expected %q, got %q", i, want, res.Headings[i].ID)
			}
		}
	})

	t.Run("document name from H1", func(t *testing.T) {
		if res.DocNode.Name != "情報分析報告" {
			t.Errorf("expected document name='情報分析報告', got %q", res.DocNode.Name)
		}
	})
}
