package extractor

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

const htmlSectionMaxBytes = 10240 // 10 KB section text cap

// hstate tracks the currently open heading element.
type hstate struct {
	level     int
	id        string
	startLine int
	buf       strings.Builder
}

// htmlState holds all mutable state for the HTML tokenizer walk.
type htmlState struct {
	relPath      string
	title        strings.Builder
	body         strings.Builder
	headings     []store.Node
	sectionTexts []string // per-section body text, parallel to headings
	sectionBuf   strings.Builder
	inSection    bool // true after the first heading has closed
	rawLinks     []parser.RawLink
	metaTuples   []store.MetadataTuple
	inHead       bool
	inTitle      bool
	inScript     bool
	inStyle      bool
	cur          *hstate
	globalIdx    int
	lineNum      int
}

func newHTMLState(relPath string) *htmlState {
	return &htmlState{relPath: relPath, lineNum: 1}
}

// handleHeadingStart flushes the previous section and opens a new heading.
func (s *htmlState) handleHeadingStart(tok html.Token) {
	if s.inSection {
		s.sectionTexts = append(s.sectionTexts, s.sectionBuf.String())
		s.sectionBuf.Reset()
	}
	level := int(tok.Data[1] - '0')
	idAttr := attrVal(tok.Attr, "id")
	var nodeID string
	if idAttr != "" {
		nodeID = s.relPath + "#" + idAttr
	} else {
		nodeID = fmt.Sprintf("%s#h%d-%d", s.relPath, level, s.globalIdx)
		s.globalIdx++
	}
	s.cur = &hstate{level: level, id: nodeID, startLine: s.lineNum}
}

// handleAnchorTag extracts href links from <a> tags.
func (s *htmlState) handleAnchorTag(tok html.Token) {
	href := attrVal(tok.Attr, "href")
	if href == "" {
		return
	}
	kind := "html_link"
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		kind = "external"
	}
	from := s.relPath
	if s.cur != nil {
		from = s.cur.id
	}
	s.rawLinks = append(s.rawLinks, parser.RawLink{
		Text: href, Target: href, Kind: kind,
		Line: s.lineNum, FromNodeID: from,
	})
}

// handleMetaTag extracts name/property + content metadata from <meta> tags.
func (s *htmlState) handleMetaTag(tok html.Token) {
	name := attrVal(tok.Attr, "name")
	prop := attrVal(tok.Attr, "property")
	content := attrVal(tok.Attr, "content")
	key := name
	if key == "" {
		key = prop
	}
	if key != "" && content != "" {
		s.metaTuples = append(s.metaTuples, store.MetadataTuple{
			Key: key, Value: content, ValueType: "string", Source: "html_meta",
		})
	}
}

// handleStartTag dispatches start/self-closing tokens.
func (s *htmlState) handleStartTag(tok html.Token) {
	switch tok.Data {
	case "head":
		s.inHead = true
	case "title":
		s.inTitle = true
	case "script":
		s.inScript = true
	case "style":
		s.inStyle = true
	case "h1", "h2", "h3", "h4", "h5", "h6":
		s.handleHeadingStart(tok)
	case "a":
		s.handleAnchorTag(tok)
	case "meta":
		s.handleMetaTag(tok)
	}
}

// handleHeadingEnd closes the current heading and records the node.
func (s *htmlState) handleHeadingEnd() {
	if s.cur == nil {
		return
	}
	s.headings = append(s.headings, store.Node{
		ID:            s.cur.id,
		Kind:          "heading",
		Name:          strings.TrimSpace(s.cur.buf.String()),
		QualifiedName: s.cur.id,
		FilePath:      s.relPath,
		StartLine:     s.cur.startLine,
		EndLine:       s.lineNum,
		Level:         s.cur.level,
		UpdatedAt:     time.Now().Unix(),
	})
	s.cur = nil
	s.inSection = true
}

// handleEndTag dispatches end tokens.
func (s *htmlState) handleEndTag(tok html.Token) {
	switch tok.Data {
	case "head":
		s.inHead, s.inTitle = false, false
	case "title":
		s.inTitle = false
	case "script":
		s.inScript = false
	case "style":
		s.inStyle = false
	case "h1", "h2", "h3", "h4", "h5", "h6":
		s.handleHeadingEnd()
	}
}

// handleTextToken processes text content tokens.
func (s *htmlState) handleTextToken(text string) {
	if s.inTitle {
		s.title.WriteString(text)
		return
	}
	if s.inScript || s.inStyle || s.inHead {
		return
	}
	if s.cur != nil {
		s.cur.buf.WriteString(text)
	} else if s.inSection {
		s.sectionBuf.WriteString(text)
	}
	s.body.WriteString(text)
}

func extractHTML(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	_ = absPath
	s := newHTMLState(relPath)

	z := html.NewTokenizer(bytes.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		for _, b := range z.Raw() {
			if b == '\n' {
				s.lineNum++
			}
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			s.handleStartTag(z.Token())
		case html.EndTagToken:
			s.handleEndTag(z.Token())
		case html.TextToken:
			s.handleTextToken(string(z.Text()))
		}
	}

	// Flush section body for the last heading.
	if s.inSection {
		s.sectionTexts = append(s.sectionTexts, s.sectionBuf.String())
	}

	titleStr := strings.TrimSpace(s.title.String())
	name := titleStr
	if name == "" {
		name = filepath.Base(relPath)
	}

	bodyStr := s.body.String()
	excerpt := bodyStr
	if len(excerpt) > 500 {
		excerpt = excerpt[:500]
		if idx := strings.LastIndex(excerpt, " "); idx > 250 {
			excerpt = excerpt[:idx]
		}
	}
	excerpt = strings.TrimSpace(excerpt)

	docEndLine := 1 + bytes.Count(src, []byte("\n"))
	docNode := store.Node{
		ID: relPath, Kind: "document", Name: name,
		QualifiedName: relPath, FilePath: relPath,
		StartLine: 1, EndLine: docEndLine,
		BodyExcerpt: excerpt, UpdatedAt: time.Now().Unix(),
	}

	htmlComputeEndLines(s.headings, docEndLine)

	return &parser.ParseResult{
		DocNode:  docNode,
		Headings: s.headings,
		Edges:    htmlContainmentEdges(relPath, s.headings),
		RawLinks: s.rawLinks,
		FileInfo: store.FileInfo{
			Path: relPath, ContentHash: hash, Size: int64(len(src)),
			ModifiedAt: 0, IndexedAt: time.Now().Unix(),
			NodeCount: 1 + len(s.headings),
		},
		SectionChunks:  htmlSectionChunks(relPath, s.headings, s.sectionTexts, hash),
		MetadataTuples: s.metaTuples,
	}, nil
}

func attrVal(attrs []html.Attribute, name string) string {
	for _, a := range attrs {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func htmlComputeEndLines(headings []store.Node, docEndLine int) {
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

func htmlContainmentEdges(docID string, headings []store.Node) []store.Edge {
	var edges []store.Edge
	type frame struct {
		level int
		id    string
	}
	stack := []frame{{0, docID}}
	for _, h := range headings {
		for len(stack) > 1 && stack[len(stack)-1].level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		edges = append(edges, store.Edge{Source: stack[len(stack)-1].id, Target: h.ID, Kind: "contains"})
		stack = append(stack, frame{h.Level, h.ID})
	}
	return edges
}

func htmlSectionChunks(relPath string, headings []store.Node, sectionTexts []string, contentHash string) []store.SectionChunk {
	type sf struct {
		level int
		name  string
	}
	var stack []sf
	var chunks []store.SectionChunk
	for i, h := range headings {
		for len(stack) > 0 && stack[len(stack)-1].level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, f := range stack {
			parts = append(parts, f.name)
		}
		parts = append(parts, h.Name)
		stack = append(stack, sf{h.Level, h.Name})
		var sectionBody string
		if i < len(sectionTexts) {
			sectionBody = strings.TrimSpace(sectionTexts[i])
		}
		text := sectionBody
		if len(text) > htmlSectionMaxBytes {
			text = text[:htmlSectionMaxBytes] + "\n[...truncated]"
		}
		sum := sha256.Sum256([]byte(text))
		chunks = append(chunks, store.SectionChunk{
			NodeID: h.ID, FilePath: relPath,
			StartLine: h.StartLine, EndLine: h.EndLine,
			ContentHash: contentHash, SectionHash: fmt.Sprintf("%x", sum),
			HeadingPath: strings.Join(parts, " > "), Text: text,
		})
	}
	return chunks
}
