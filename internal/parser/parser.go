package parser

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	goldparser "github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/Detective-XH/docgraph/internal/store"
)

// RawLink represents a link found during parsing, before resolution.
type RawLink struct {
	Text       string // Display text
	Target     string // Link target (path, wikilink name, URL)
	Kind       string // "wikilink", "markdown_link", "embed", "external"
	Line       int
	FromNodeID string // ID of the containing node
}

// ParseResult contains all data extracted from a single markdown file.
type ParseResult struct {
	DocNode  store.Node
	Headings []store.Node
	Defs     []store.Node
	Tags     []store.Node // Deduplicated tag nodes
	Edges    []store.Edge
	RawLinks []RawLink
	FileInfo store.FileInfo
}

var inlineWikilinkRe = regexp.MustCompile(`(!?)\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)

// ParseFile parses a markdown file and extracts nodes, edges, and raw links.
func ParseFile(absPath string, relPath string, source []byte, contentHash string) (*ParseResult, error) {
	// 1. Extract frontmatter
	fm, err := ExtractFrontmatter(source)
	if err != nil {
		return nil, err
	}

	// Pre-compute line offsets for byte-offset → line-number conversion.
	lineOffsets := buildLineOffsets(source)

	// 2. Parse markdown AST
	md := goldmark.New(goldmark.WithExtensions(MetaExt))
	ctx := goldparser.NewContext()
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader, goldparser.WithContext(ctx))

	// 3. Create document node
	docEndLine := len(lineOffsets)
	bodyExcerpt := truncate(string(bodyAfterFrontmatter(source)), 500)

	docNode := store.Node{
		ID:            relPath,
		Kind:          "document",
		QualifiedName: relPath,
		FilePath:      relPath,
		StartLine:     1,
		EndLine:       docEndLine,
		Metadata:      FrontmatterToJSON(fm),
		BodyExcerpt:   bodyExcerpt,
		UpdatedAt:     time.Now().Unix(),
	}

	// Pre-parse: scan raw source for [[wikilinks]] (goldmark splits [[ across nodes)
	var preLinks []RawLink
	body := bodyAfterFrontmatter(source)
	bodyStartLine := docEndLine - bytes.Count(body, []byte("\n"))
	for i, line := range bytes.Split(body, []byte("\n")) {
		lineNum := bodyStartLine + i
		for _, m := range inlineWikilinkRe.FindAllSubmatch(line, -1) {
			prefix := string(m[1])
			target := string(m[2])
			kind := "wikilink"
			if prefix == "!" {
				kind = "embed"
			}
			preLinks = append(preLinks, RawLink{
				Text: target, Target: target, Kind: kind,
				Line: lineNum, FromNodeID: relPath,
			})
		}
	}

	// 4. Walk AST
	var headings []store.Node
	var rawLinks []RawLink
	var firstH1 string
	slugCount := make(map[string]int)

	currentHeadingID := docNode.ID
	currentBlockLine := 1

	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
			seg := n.Lines().At(0)
			currentBlockLine = offsetToLine(lineOffsets, seg.Start)
		}

		switch v := n.(type) {
		case *ast.Heading:
			txt := extractText(v, source)
			slug := slugify(txt)
			slugCount[slug]++
			if slugCount[slug] > 1 {
				slug = fmt.Sprintf("%s-%d", slug, slugCount[slug])
			}
			id := relPath + "#" + slug
			startLine := currentBlockLine

			if v.Level == 1 && firstH1 == "" {
				firstH1 = txt
			}

			headings = append(headings, store.Node{
				ID:            id,
				Kind:          "heading",
				Name:          txt,
				QualifiedName: relPath + "#" + slug,
				FilePath:      relPath,
				StartLine:     startLine,
				Level:         v.Level,
				UpdatedAt:     time.Now().Unix(),
			})
			currentHeadingID = id

		case *ast.Link:
			dest := string(v.Destination)
			linkText := extractText(v, source)
			kind := classifyLink(dest)
			rawLinks = append(rawLinks, RawLink{
				Text:       linkText,
				Target:     dest,
				Kind:       kind,
				Line:       currentBlockLine,
				FromNodeID: currentHeadingID,
			})

		case *ast.AutoLink:
			url := string(v.URL(source))
			rawLinks = append(rawLinks, RawLink{
				Text:   url,
				Target: url,
				Kind:   "external",
				Line:   currentBlockLine,
				FromNodeID: currentHeadingID,
			})

		case *ast.Text:
			// Wikilinks handled by pre-parse pass (goldmark splits [[ across Text nodes)
		}

		return ast.WalkContinue, nil
	})

	// Determine document name: first H1 > frontmatter title > filename
	docNode.Name = firstH1
	if docNode.Name == "" {
		docNode.Name = GetTitle(fm)
	}
	if docNode.Name == "" {
		base := filepath.Base(relPath)
		docNode.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	// 5. Post-pass: compute heading end_lines
	computeHeadingEndLines(headings, docEndLine)

	// 6. Create containment edges
	edges := buildContainmentEdges(docNode.ID, headings)

	// 7. Create tag nodes and tagged edges
	var tagNodes []store.Node
	seen := make(map[string]bool)
	for _, tag := range GetTags(fm) {
		tagID := "tag:" + strings.ToLower(tag)
		if seen[tagID] {
			continue
		}
		seen[tagID] = true
		tagNodes = append(tagNodes, store.Node{
			ID:            tagID,
			Kind:          "tag",
			Name:          tag,
			QualifiedName: tagID,
			FilePath:      relPath,
			StartLine:     0,
			EndLine:       0,
			UpdatedAt:     time.Now().Unix(),
		})
		edges = append(edges, store.Edge{
			Source: docNode.ID,
			Target: tagID,
			Kind:   "tagged",
		})
	}

	// 8. Frontmatter wikilinks → RawLinks
	for _, target := range GetWikilinks(fm) {
		rawLinks = append(rawLinks, RawLink{
			Kind:       "wikilink",
			Target:     target,
			FromNodeID: docNode.ID,
		})
	}

	// 8b. Merge pre-parsed inline wikilinks (assign to nearest heading)
	for _, pl := range preLinks {
		pl.FromNodeID = docNode.ID
		for i := len(headings) - 1; i >= 0; i-- {
			if headings[i].StartLine <= pl.Line {
				pl.FromNodeID = headings[i].ID
				break
			}
		}
		rawLinks = append(rawLinks, pl)
	}

	// 9. Build FileInfo
	fileInfo := store.FileInfo{
		Path:           relPath,
		ContentHash:    contentHash,
		Size:           int64(len(source)),
		ModifiedAt:     0, // caller sets this
		IndexedAt:      time.Now().Unix(),
		NodeCount:      1 + len(headings) + len(tagNodes),
		HasFrontmatter: fm != nil,
	}

	return &ParseResult{
		DocNode:  docNode,
		Headings: headings,
		Defs:     nil, // reserved for future definition extraction
		Tags:     tagNodes,
		Edges:    edges,
		RawLinks: rawLinks,
		FileInfo: fileInfo,
	}, nil
}

// buildLineOffsets returns a slice where index i is the byte offset of line i+1.
// lineOffsets[0] = 0 (line 1 starts at byte 0).
func buildLineOffsets(source []byte) []int {
	offsets := []int{0}
	for i, b := range source {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// offsetToLine converts a byte offset to a 1-based line number.
func offsetToLine(offsets []int, byteOffset int) int {
	// Binary search: find the last offset <= byteOffset
	line := sort.Search(len(offsets), func(i int) bool {
		return offsets[i] > byteOffset
	})
	if line == 0 {
		return 1
	}
	return line
}

// extractText walks the children of n and collects text content.
func extractText(n ast.Node, source []byte) string {
	var buf bytes.Buffer
	ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if t, ok := node.(*ast.Text); ok {
				buf.Write(t.Segment.Value(source))
			}
		}
		return ast.WalkContinue, nil
	})
	return buf.String()
}

// slugify converts a heading text to a URL-friendly slug.
// Lowercase, replace whitespace runs with single "-", keep Unicode letters + digits + "-".
func slugify(s string) string {
	s = strings.ToLower(s)
	var buf strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevDash {
				buf.WriteRune('-')
				prevDash = true
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			buf.WriteRune(r)
			prevDash = false
		}
	}
	return strings.Trim(buf.String(), "-")
}

// classifyLink determines the kind of a markdown link by its destination.
func classifyLink(dest string) string {
	if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") {
		return "external"
	}
	return "markdown_link"
}

// computeHeadingEndLines sets EndLine for each heading based on the next heading or document end.
func computeHeadingEndLines(headings []store.Node, docEndLine int) {
	sort.Slice(headings, func(i, j int) bool {
		return headings[i].StartLine < headings[j].StartLine
	})
	for i := range headings {
		headings[i].EndLine = docEndLine
		for j := i + 1; j < len(headings); j++ {
			if headings[j].Level <= headings[i].Level {
				headings[i].EndLine = headings[j].StartLine - 1
				break
			}
		}
	}
}

// buildContainmentEdges creates "contains" edges using a stack-based approach.
func buildContainmentEdges(docID string, headings []store.Node) []store.Edge {
	var edges []store.Edge
	// Stack of (level, nodeID). Document is level 0.
	type frame struct {
		level int
		id    string
	}
	stack := []frame{{level: 0, id: docID}}

	for _, h := range headings {
		// Pop headings with level >= current heading's level
		for len(stack) > 1 && stack[len(stack)-1].level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		parentID := stack[len(stack)-1].id
		edges = append(edges, store.Edge{
			Source: parentID,
			Target: h.ID,
			Kind:   "contains",
		})
		stack = append(stack, frame{level: h.Level, id: h.ID})
	}

	return edges
}

// truncate returns at most maxBytes bytes of s, cutting at the last space before the limit.
func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	if idx := strings.LastIndex(s, " "); idx > maxBytes/2 {
		s = s[:idx]
	}
	return s
}
