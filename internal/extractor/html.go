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

func extractHTML(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	_ = absPath
	type hstate struct {
		level     int
		id        string
		startLine int
		buf       strings.Builder
	}
	var (
		title, body                        strings.Builder
		headings                           []store.Node
		sectionTexts                       []string // per-section body text, parallel to headings
		sectionBuf                         strings.Builder
		inSection                          bool // true after the first heading has closed
		rawLinks                           []parser.RawLink
		metaTuples                         []store.MetadataTuple
		inHead, inTitle, inScript, inStyle bool
		cur                                *hstate
		globalIdx                          int
		lineNum                            = 1
	)

	z := html.NewTokenizer(bytes.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		for _, b := range z.Raw() {
			if b == '\n' {
				lineNum++
			}
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch tok.Data {
			case "head":
				inHead = true
			case "title":
				inTitle = true
			case "script":
				inScript = true
			case "style":
				inStyle = true
			case "h1", "h2", "h3", "h4", "h5", "h6":
				// Flush section body accumulated since the previous heading closed.
				if inSection {
					sectionTexts = append(sectionTexts, sectionBuf.String())
					sectionBuf.Reset()
				}
				level := int(tok.Data[1] - '0')
				idAttr := attrVal(tok.Attr, "id")
				var nodeID string
				if idAttr != "" {
					nodeID = relPath + "#" + idAttr
				} else {
					nodeID = fmt.Sprintf("%s#h%d-%d", relPath, level, globalIdx)
					globalIdx++
				}
				cur = &hstate{level: level, id: nodeID, startLine: lineNum}
			case "a":
				if href := attrVal(tok.Attr, "href"); href != "" {
					kind := "html_link"
					if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
						kind = "external"
					}
					from := relPath
					if cur != nil {
						from = cur.id
					}
					rawLinks = append(rawLinks, parser.RawLink{
						Text: href, Target: href, Kind: kind,
						Line: lineNum, FromNodeID: from,
					})
				}
			case "meta":
				name := attrVal(tok.Attr, "name")
				prop := attrVal(tok.Attr, "property")
				content := attrVal(tok.Attr, "content")
				key := name
				if key == "" {
					key = prop
				}
				if key != "" && content != "" {
					metaTuples = append(metaTuples, store.MetadataTuple{
						Key: key, Value: content, ValueType: "string", Source: "html_meta",
					})
				}
			}

		case html.EndTagToken:
			tok := z.Token()
			switch tok.Data {
			case "head":
				inHead, inTitle = false, false
			case "title":
				inTitle = false
			case "script":
				inScript = false
			case "style":
				inStyle = false
			case "h1", "h2", "h3", "h4", "h5", "h6":
				if cur != nil {
					headings = append(headings, store.Node{
						ID:            cur.id,
						Kind:          "heading",
						Name:          strings.TrimSpace(cur.buf.String()),
						QualifiedName: cur.id,
						FilePath:      relPath,
						StartLine:     cur.startLine,
						EndLine:       lineNum,
						Level:         cur.level,
						UpdatedAt:     time.Now().Unix(),
					})
					cur = nil
					inSection = true
				}
			}

		case html.TextToken:
			text := string(z.Text())
			if inTitle {
				title.WriteString(text)
				continue
			}
			if inScript || inStyle || inHead {
				continue
			}
			if cur != nil {
				cur.buf.WriteString(text)
			} else if inSection {
				sectionBuf.WriteString(text)
			}
			body.WriteString(text)
		}
	}

	// Flush section body for the last heading.
	if inSection {
		sectionTexts = append(sectionTexts, sectionBuf.String())
	}

	titleStr := strings.TrimSpace(title.String())
	name := titleStr
	if name == "" {
		name = filepath.Base(relPath)
	}

	bodyStr := body.String()
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

	htmlComputeEndLines(headings, docEndLine)

	return &parser.ParseResult{
		DocNode:  docNode,
		Headings: headings,
		Edges:    htmlContainmentEdges(relPath, headings),
		RawLinks: rawLinks,
		FileInfo: store.FileInfo{
			Path: relPath, ContentHash: hash, Size: int64(len(src)),
			ModifiedAt: 0, IndexedAt: time.Now().Unix(),
			NodeCount: 1 + len(headings),
		},
		SectionChunks:  htmlSectionChunks(relPath, headings, sectionTexts, hash),
		MetadataTuples: metaTuples,
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
