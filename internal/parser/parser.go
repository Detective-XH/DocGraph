package parser

import (
	"bytes"
	"crypto/sha256"
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
	DocNode         store.Node
	Headings        []store.Node
	Defs            []store.Node
	Tags            []store.Node // Deduplicated tag nodes
	Edges           []store.Edge
	RawLinks        []RawLink
	FileInfo        store.FileInfo
	SectionChunks   []store.SectionChunk
	MetadataTuples  []store.MetadataTuple // Normalized key/value pairs from frontmatter
}

var inlineWikilinkRe = regexp.MustCompile(`(!?)\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
var definitionLineRe = regexp.MustCompile(`^\s*\*\*([^*:\n][^*\n]{0,120}?):\*\*\s*(.*)$`)

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

	// Pre-parse: scan raw source for [[wikilinks]] (goldmark splits [[ across nodes).
	body := bodyAfterFrontmatter(source)
	bodyStartLine := docEndLine - bytes.Count(body, []byte("\n"))
	preLinks := scanInlineWikilinks(body, bodyStartLine, relPath)

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
				Text:       url,
				Target:     url,
				Kind:       "external",
				Line:       currentBlockLine,
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

	// 5b. Build section chunks (document + heading nodes).
	sectionChunks := buildSectionChunks(source, docNode, headings, contentHash)

	// 6. Create containment edges
	edges := buildContainmentEdges(docNode.ID, headings)
	defs, defEdges := extractDefinitions(body, bodyStartLine, relPath, docNode.ID, headings)
	edges = append(edges, defEdges...)

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
		NodeCount:      1 + len(headings) + len(defs) + len(tagNodes),
		HasFrontmatter: fm != nil,
	}

	return &ParseResult{
		DocNode:        docNode,
		Headings:       headings,
		Defs:           defs,
		Tags:           tagNodes,
		Edges:          edges,
		RawLinks:       rawLinks,
		FileInfo:       fileInfo,
		SectionChunks:  sectionChunks,
		MetadataTuples: ExtractMetadataTuples(fm),
	}, nil
}

func extractDefinitions(body []byte, bodyStartLine int, relPath, docID string, headings []store.Node) ([]store.Node, []store.Edge) {
	var defs []store.Node
	var edges []store.Edge
	var inFence bool
	var fenceMarker string
	var inHTMLComment bool
	slugCount := make(map[string]int)

	for i, rawLine := range bytes.Split(body, []byte("\n")) {
		lineNum := bodyStartLine + i
		trimmed := bytes.TrimSpace(rawLine)

		if marker, ok := fenceMarkerFromLine(trimmed); ok {
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			continue
		}

		line := stripHTMLComments(rawLine, &inHTMLComment)
		m := definitionLineRe.FindSubmatch(line)
		if m == nil {
			continue
		}

		term := strings.TrimSpace(string(m[1]))
		definition := strings.TrimSpace(string(m[2]))
		if term == "" || definition == "" {
			continue
		}

		slug := "def-" + slugify(term)
		slugCount[slug]++
		if slugCount[slug] > 1 {
			slug = fmt.Sprintf("%s-%d", slug, slugCount[slug])
		}
		id := relPath + "#" + slug
		parentID := nearestHeadingID(headings, lineNum, docID)

		defs = append(defs, store.Node{
			ID:            id,
			Kind:          "definition",
			Name:          term,
			QualifiedName: id,
			FilePath:      relPath,
			StartLine:     lineNum,
			EndLine:       lineNum,
			BodyExcerpt:   definition,
			UpdatedAt:     time.Now().Unix(),
		})
		edges = append(edges, store.Edge{
			Source: parentID,
			Target: id,
			Kind:   "contains",
			Line:   lineNum,
		})
	}

	return defs, edges
}

func nearestHeadingID(headings []store.Node, lineNum int, docID string) string {
	for i := len(headings) - 1; i >= 0; i-- {
		if headings[i].StartLine <= lineNum {
			return headings[i].ID
		}
	}
	return docID
}

func scanInlineWikilinks(body []byte, bodyStartLine int, relPath string) []RawLink {
	var links []RawLink
	var inFence bool
	var fenceMarker string
	var inHTMLComment bool

	for i, rawLine := range bytes.Split(body, []byte("\n")) {
		lineNum := bodyStartLine + i
		trimmed := bytes.TrimSpace(rawLine)

		if marker, ok := fenceMarkerFromLine(trimmed); ok {
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			continue
		}

		line := stripHTMLComments(rawLine, &inHTMLComment)
		for _, m := range inlineWikilinkRe.FindAllSubmatch(line, -1) {
			prefix := string(m[1])
			target := string(m[2])
			kind := "wikilink"
			if prefix == "!" {
				kind = "embed"
			}
			links = append(links, RawLink{
				Text: target, Target: target, Kind: kind,
				Line: lineNum, FromNodeID: relPath,
			})
		}
	}

	return links
}

func fenceMarkerFromLine(line []byte) (string, bool) {
	if bytes.HasPrefix(line, []byte("```")) {
		return "```", true
	}
	if bytes.HasPrefix(line, []byte("~~~")) {
		return "~~~", true
	}
	return "", false
}

func stripHTMLComments(line []byte, inComment *bool) []byte {
	var out []byte
	for len(line) > 0 {
		if *inComment {
			end := bytes.Index(line, []byte("-->"))
			if end == -1 {
				return out
			}
			line = line[end+len("-->"):]
			*inComment = false
			continue
		}

		start := bytes.Index(line, []byte("<!--"))
		if start == -1 {
			out = append(out, line...)
			return out
		}
		out = append(out, line[:start]...)
		line = line[start+len("<!--"):]
		*inComment = true
	}
	return out
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

const sectionMaxBytes = 10240

// extractSectionText extracts lines [startLine, endLine] (1-based, inclusive) from source
// using the same logic as ReadSectionContent: split on "\n", slice, join with "\n".
// Returns the text capped at sectionMaxBytes with a truncation marker if needed.
func extractSectionText(source []byte, startLine, endLine int) string {
	lines := strings.Split(string(source), "\n")
	start := startLine - 1
	if start < 0 {
		start = 0
	}
	end := endLine
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}
	text := strings.Join(lines[start:end], "\n")
	if len(text) > sectionMaxBytes {
		text = text[:sectionMaxBytes] + "\n[...truncated]"
	}
	return text
}

// sectionHash computes SHA-256 of the given text, returning a hex string.
func sectionHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum)
}

// buildSectionChunks creates SectionChunk entries for the document node and all heading nodes.
// Headings must already be sorted by StartLine (computeHeadingEndLines guarantees this).
func buildSectionChunks(source []byte, docNode store.Node, headings []store.Node, contentHash string) []store.SectionChunk {
	const cap_ = sectionMaxBytes

	// Document-level chunk: entire file content.
	docText := extractSectionText(source, docNode.StartLine, docNode.EndLine)
	chunks := []store.SectionChunk{
		{
			NodeID:      docNode.ID,
			FilePath:    docNode.FilePath,
			StartLine:   docNode.StartLine,
			EndLine:     docNode.EndLine,
			ContentHash: contentHash,
			SectionHash: sectionHash(docText),
			HeadingPath: "",
			Text:        docText,
		},
	}

	// Build heading_path using a parent stack.
	type stackFrame struct {
		level int
		name  string
	}
	var stack []stackFrame

	for _, h := range headings {
		// Pop stack entries with level >= current heading level.
		for len(stack) > 0 && stack[len(stack)-1].level >= h.Level {
			stack = stack[:len(stack)-1]
		}

		// Build breadcrumb from remaining stack + current heading name.
		parts := make([]string, 0, len(stack)+1)
		for _, f := range stack {
			parts = append(parts, f.name)
		}
		parts = append(parts, h.Name)
		headingPath := strings.Join(parts, " > ")

		// Push current heading onto stack.
		stack = append(stack, stackFrame{level: h.Level, name: h.Name})

		text := extractSectionText(source, h.StartLine, h.EndLine)
		chunks = append(chunks, store.SectionChunk{
			NodeID:      h.ID,
			FilePath:    h.FilePath,
			StartLine:   h.StartLine,
			EndLine:     h.EndLine,
			ContentHash: contentHash,
			SectionHash: sectionHash(text),
			HeadingPath: headingPath,
			Text:        text,
		})
	}

	_ = cap_
	return chunks
}
