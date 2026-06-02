package extractor

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// Security limits.
const (
	docxMaxDocumentXML = 30 * 1024 * 1024 // 30 MB for word/document.xml
	docxMaxOtherEntry  = 1 * 1024 * 1024  // 1 MB for all other entries
	docxMaxTotalBytes  = 50 * 1024 * 1024 // 50 MB total uncompressed budget
	docxMaxEntries     = 500
	docxBodyExcerptCap = 500
	docxSectionTextCap = 10 * 1024 // 10 KB section text cap
)

// headingStyleRe matches Heading1..Heading6 and variants like "Heading 1", "heading-1".
var headingStyleRe = regexp.MustCompile(`(?i)^heading[\s\-_]?([1-6])$`)

// ── XML structs ──────────────────────────────────────────────────────────────

// docRels is word/_rels/document.xml.rels
type docRels struct {
	Relationships []relEntry `xml:"Relationship"`
}

type relEntry struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
	Type   string `xml:"Type,attr"`
}

// coreProps is docProps/core.xml (Dublin Core)
type coreProps struct {
	Title       string `xml:"title"`
	Creator     string `xml:"creator"`
	Description string `xml:"description"`
	Created     string `xml:"created"`
	Modified    string `xml:"modified"`
}

// docBody is word/document.xml — only the body paragraphs.
type docBody struct {
	Body struct {
		Paragraphs []paragraph `xml:"p"`
	} `xml:"body"`
}

type paragraph struct {
	Props      paraProps   `xml:"pPr"`
	Runs       []run       `xml:"r"`
	Hyperlinks []hyperlink `xml:"hyperlink"`
}

type paraProps struct {
	Style styleRef `xml:"pStyle"`
}

type styleRef struct {
	Val string `xml:"val,attr"`
}

type run struct {
	Texts []textRun `xml:"t"`
}

type textRun struct {
	Text string `xml:",chardata"`
}

type hyperlink struct {
	RID  string `xml:"id,attr"`
	Runs []run  `xml:"r"`
}

// ── helpers ──────────────────────────────────────────────────────────────────

// docxSlug converts heading text to a URL-safe slug (lowercase, spaces → '-',
// strip non-alphanumeric except '-').
func docxSlug(s string) string {
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

func docxSectionHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum)
}

// capText truncates s to at most max bytes, appending a truncation marker.
// Used for SectionChunk.Text (10 KB cap). Not used for BodyExcerpt.
func capText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[...truncated]"
}

// truncateExcerpt truncates s to at most max bytes without a marker.
// Used for BodyExcerpt (500 byte cap).
func truncateExcerpt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	if idx := strings.LastIndex(s, " "); idx > max/2 {
		s = s[:idx]
	}
	return s
}

// readZipEntry reads a zip file entry with a per-entry LimitReader.
// Returns an error if the entry exceeds limit bytes.
func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()
	lr := io.LimitReader(rc, limit+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("read zip entry %q: %w", f.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("zip entry %q exceeds size limit of %d bytes", f.Name, limit)
	}
	return data, nil
}

// isMaliciousPath returns true for zip-slip entries (absolute paths or path traversal).
func isMaliciousPath(name string) bool {
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return true
	}
	if strings.Contains(name, "..") {
		return true
	}
	if strings.Contains(name, "\\") {
		return true
	}
	return false
}

// runText concatenates all text in a run.
func runText(r run) string {
	var sb strings.Builder
	for _, t := range r.Texts {
		sb.WriteString(t.Text)
	}
	return sb.String()
}

// paraText concatenates all text in a paragraph (runs + hyperlink runs).
func paraText(p paragraph) string {
	var sb strings.Builder
	for _, r := range p.Runs {
		sb.WriteString(runText(r))
	}
	for _, h := range p.Hyperlinks {
		for _, r := range h.Runs {
			sb.WriteString(runText(r))
		}
	}
	return sb.String()
}

// headingLevel returns the heading level (1–6) for a paragraph style, or 0 if not a heading.
func headingLevel(styleVal string) int {
	m := headingStyleRe.FindStringSubmatch(styleVal)
	if m == nil {
		return 0
	}
	lvl := int(m[1][0] - '0')
	return lvl
}

// ── main extractor ────────────────────────────────────────────────────────────

func extractDOCX(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	_ = absPath
	// Open ZIP from in-memory bytes.
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, fmt.Errorf("docx: not a valid zip: %w", err)
	}

	// Security: entry count limit.
	if len(zr.File) > docxMaxEntries {
		return nil, fmt.Errorf("docx: zip entry count %d exceeds limit of %d", len(zr.File), docxMaxEntries)
	}

	// Security: zip-slip — scan all entries upfront.
	for _, f := range zr.File {
		if isMaliciousPath(f.Name) {
			return nil, fmt.Errorf("docx: malicious zip entry path %q", f.Name)
		}
	}

	// Read relevant entries, enforcing per-entry and total budget.
	var (
		rawDocument []byte
		rawRels     []byte
		rawCore     []byte
		totalBytes  int64
	)

	for _, f := range zr.File {
		switch f.Name {
		case "word/document.xml":
			rawDocument, err = readZipEntry(f, docxMaxDocumentXML)
			if err != nil {
				return nil, fmt.Errorf("docx: %w", err)
			}
			totalBytes += int64(len(rawDocument))
		case "word/_rels/document.xml.rels":
			rawRels, err = readZipEntry(f, docxMaxOtherEntry)
			if err != nil {
				return nil, fmt.Errorf("docx: %w", err)
			}
			totalBytes += int64(len(rawRels))
		case "docProps/core.xml":
			rawCore, err = readZipEntry(f, docxMaxOtherEntry)
			if err != nil {
				return nil, fmt.Errorf("docx: %w", err)
			}
			totalBytes += int64(len(rawCore))
		default:
			// Still count other entries against budget; read header only via UncompressedSize64.
			totalBytes += int64(f.UncompressedSize64) // #nosec G115 -- non-named zip entries are counted but never decompressed; named entries are capped by readZipEntry's LimitReader, so an overflowed accumulator cannot create a decompression-bomb path
		}
		if totalBytes > docxMaxTotalBytes {
			return nil, errors.New("docx: total uncompressed size exceeds 50 MB budget")
		}
	}

	// Parse relationships.
	rels := make(map[string]string) // rId → URL
	if rawRels != nil {
		var dr docRels
		if err := xml.Unmarshal(rawRels, &dr); err == nil {
			for _, r := range dr.Relationships {
				if strings.HasPrefix(r.Target, "http://") || strings.HasPrefix(r.Target, "https://") {
					rels[r.ID] = r.Target
				}
			}
		}
	}

	// Parse core.xml metadata.
	var core coreProps
	if rawCore != nil {
		// core.xml uses Dublin Core namespaces; encoding/xml matches by local name.
		_ = xml.Unmarshal(rawCore, &core)
	}

	// Parse document.xml.
	var body docBody
	if rawDocument != nil {
		if err := xml.Unmarshal(rawDocument, &body); err != nil {
			return nil, fmt.Errorf("docx: parse word/document.xml: %w", err)
		}
	}

	// Determine document title.
	docTitle := core.Title
	if docTitle == "" {
		base := filepath.Base(relPath)
		docTitle = strings.TrimSuffix(base, filepath.Ext(base))
	}

	now := time.Now().Unix()

	// Walk paragraphs: collect headings, body text, hyperlinks.
	var (
		headings   []store.Node
		rawLinks   []parser.RawLink
		bodyParts  []string
		slugCounts = make(map[string]int)
	)

	for _, p := range body.Body.Paragraphs {
		txt := paraText(p)
		lvl := headingLevel(p.Props.Style.Val)

		if lvl > 0 && strings.TrimSpace(txt) != "" {
			slug := "heading-" + docxSlug(txt)
			slugCounts[slug]++
			idx := slugCounts[slug] - 1
			nodeID := fmt.Sprintf("%s#%s-%d", relPath, slug, idx)

			headings = append(headings, store.Node{
				ID:            nodeID,
				Kind:          "heading",
				Name:          txt,
				QualifiedName: nodeID,
				FilePath:      relPath,
				StartLine:     0,
				EndLine:       0,
				Level:         lvl,
				UpdatedAt:     now,
			})
		}

		// Collect body text for excerpt.
		if strings.TrimSpace(txt) != "" {
			bodyParts = append(bodyParts, txt)
		}

		// Hyperlinks.
		for _, hl := range p.Hyperlinks {
			if url, ok := rels[hl.RID]; ok {
				var hlBuilder strings.Builder
				for _, r := range hl.Runs {
					hlBuilder.WriteString(runText(r))
				}
				hlText := hlBuilder.String()
				rawLinks = append(rawLinks, parser.RawLink{
					Text:       hlText,
					Target:     url,
					Kind:       "docx_hyperlink",
					Line:       0,
					FromNodeID: relPath,
				})
			}
		}
	}

	bodyExcerpt := truncateExcerpt(strings.Join(bodyParts, " "), docxBodyExcerptCap)

	// Build DocNode.
	docNode := store.Node{
		ID:            relPath,
		Kind:          "document",
		Name:          docTitle,
		QualifiedName: relPath,
		FilePath:      relPath,
		StartLine:     1,
		EndLine:       1,
		BodyExcerpt:   bodyExcerpt,
		UpdatedAt:     now,
	}

	// Build containment edges.
	edges := buildDocxContainmentEdges(docNode.ID, headings)

	// Build MetadataTuples from core.xml.
	var metaTuples []store.MetadataTuple
	addMeta := func(key, value, valueType string) {
		if value != "" {
			metaTuples = append(metaTuples, store.MetadataTuple{
				Key:       key,
				Value:     value,
				ValueType: valueType,
				Source:    "docx_core_xml",
			})
		}
	}
	addMeta("title", core.Title, "string")
	addMeta("creator", core.Creator, "string")
	addMeta("description", core.Description, "string")
	addMeta("created", core.Created, "date")
	addMeta("modified", core.Modified, "date")

	// Build SectionChunks: document chunk + one per heading.
	sectionChunks := buildDocxSectionChunks(docNode, headings, hash, bodyParts)

	fileInfo := store.FileInfo{
		Path:           relPath,
		ContentHash:    hash,
		Size:           int64(len(src)),
		IndexedAt:      now,
		NodeCount:      1 + len(headings),
		HasFrontmatter: false,
	}

	return &parser.ParseResult{
		DocNode:        docNode,
		Headings:       headings,
		Edges:          edges,
		RawLinks:       rawLinks,
		FileInfo:       fileInfo,
		SectionChunks:  sectionChunks,
		MetadataTuples: metaTuples,
	}, nil
}

// buildDocxContainmentEdges builds "contains" edges from the document node to headings,
// using a stack to model parent-child nesting.
func buildDocxContainmentEdges(docID string, headings []store.Node) []store.Edge {
	var edges []store.Edge
	type frame struct {
		level int
		id    string
	}
	stack := []frame{{level: 0, id: docID}}

	for _, h := range headings {
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

// buildDocxSectionChunks creates one chunk for the document and one per heading.
// Since DOCX has no line numbers, we use 0/0 for start/end and represent text
// as the concatenation of all body text for the document chunk, and heading name
// for heading chunks.
func buildDocxSectionChunks(docNode store.Node, headings []store.Node, contentHash string, bodyParts []string) []store.SectionChunk {
	// Document chunk: full body text (10 KB cap).
	docText := capText(strings.Join(bodyParts, "\n"), docxSectionTextCap)
	chunks := []store.SectionChunk{
		{
			NodeID:      docNode.ID,
			FilePath:    docNode.FilePath,
			StartLine:   0,
			EndLine:     0,
			ContentHash: contentHash,
			SectionHash: docxSectionHash(docText),
			HeadingPath: "",
			Text:        docText,
		},
	}

	// Heading path breadcrumb builder.
	type stackFrame struct {
		level int
		name  string
	}
	var stack []stackFrame

	for _, h := range headings {
		for len(stack) > 0 && stack[len(stack)-1].level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, f := range stack {
			parts = append(parts, f.name)
		}
		parts = append(parts, h.Name)
		headingPath := strings.Join(parts, " > ")
		stack = append(stack, stackFrame{level: h.Level, name: h.Name})

		text := capText(h.Name, docxSectionTextCap)
		chunks = append(chunks, store.SectionChunk{
			NodeID:      h.ID,
			FilePath:    h.FilePath,
			StartLine:   0,
			EndLine:     0,
			ContentHash: contentHash,
			SectionHash: docxSectionHash(text),
			HeadingPath: headingPath,
			Text:        text,
		})
	}

	return chunks
}
