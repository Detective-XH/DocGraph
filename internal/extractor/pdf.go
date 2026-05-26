package extractor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

const (
	pdfMaxPages    = 500
	pdfMaxTextLen  = 10 * 1024 // 10 KB per section chunk
	pdfExcerptLen  = 500       // 500 byte body excerpt cap
	pdfScannedThreshold = 50   // avg chars/page below which we flag as image-only
)

func extractPDF(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	// Write src to a temp file since ledongthuc/pdf requires a file path.
	tmp, err := os.CreateTemp("", "docgraph-pdf-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("extractPDF: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(src); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("extractPDF: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("extractPDF: close temp file: %w", err)
	}

	f, r, err := pdf.Open(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("extractPDF: open PDF: %w", err)
	}
	defer f.Close()

	numPages := r.NumPage()
	if numPages > pdfMaxPages {
		return nil, fmt.Errorf("extractPDF: PDF has %d pages, exceeds cap of %d", numPages, pdfMaxPages)
	}

	// --- Extract Info dict (Trailer → Info, not Trailer → Root → Info) ---
	info := r.Trailer().Key("Info")
	docTitle := strings.TrimSpace(info.Key("Title").Text())
	docAuthor := strings.TrimSpace(info.Key("Author").Text())
	docSubject := strings.TrimSpace(info.Key("Subject").Text())
	docKeywords := strings.TrimSpace(info.Key("Keywords").Text())
	docCreationDate := strings.TrimSpace(info.Key("CreationDate").Text())

	// Determine display name: prefer title from info dict, else basename.
	docName := docTitle
	if docName == "" {
		docName = filepath.Base(relPath)
	}

	// --- Extract per-page text and links ---
	fonts := make(map[string]*pdf.Font)
	var chunks []store.SectionChunk
	var rawLinks []parser.RawLink
	var totalChars int

	pageTexts := make([]string, numPages)
	for n := 1; n <= numPages; n++ {
		page := r.Page(n)
		// Cache fonts for performance.
		for _, name := range page.Fonts() {
			if _, ok := fonts[name]; !ok {
				f := page.Font(name)
				fonts[name] = &f
			}
		}
		text, _ := page.GetPlainText(fonts)
		pageTexts[n-1] = text
		totalChars += len(text)

		// Extract URI annotations from this page.
		rawLinks = append(rawLinks, extractPageURIs(page, relPath, n)...)
	}

	// --- Scanned PDF detection ---
	avgChars := 0
	if numPages > 0 {
		avgChars = totalChars / numPages
	}
	isScanned := numPages > 0 && avgChars < pdfScannedThreshold

	// --- Body excerpt from page 1 text (500 byte cap) ---
	var bodyExcerpt string
	if len(pageTexts) > 0 {
		t := strings.TrimSpace(pageTexts[0])
		if len(t) > pdfExcerptLen {
			t = t[:pdfExcerptLen]
		}
		bodyExcerpt = t
	}

	// --- Build page nodes, section chunks, and containment edges ---
	now := time.Now().Unix()
	var pageNodes []store.Node
	var edges []store.Edge
	if isScanned {
		// Single document-level chunk for image-only PDFs; no page nodes needed.
		chunks = []store.SectionChunk{
			{
				NodeID:      relPath,
				FilePath:    relPath,
				StartLine:   1,
				EndLine:     1,
				ContentHash: hash,
				SectionHash: sectionHash(""),
				HeadingPath: "",
				Text:        "",
			},
		}
	} else {
		for n, text := range pageTexts {
			pageNum := n + 1
			nodeID := relPath + "#page-" + strconv.Itoa(pageNum)
			bounded := text
			if len(bounded) > pdfMaxTextLen {
				bounded = bounded[:pdfMaxTextLen]
			}
			// Page nodes serve as section anchors (plan: "page nodes serve as the section anchor").
			// They must exist in nodes before UpsertSectionChunks due to FK constraint.
			pageNodes = append(pageNodes, store.Node{
				ID:            nodeID,
				Kind:          "heading",
				Name:          "Page " + strconv.Itoa(pageNum),
				QualifiedName: nodeID,
				FilePath:      relPath,
				StartLine:     pageNum,
				EndLine:       pageNum,
				Level:         1,
				UpdatedAt:     now,
			})
			edges = append(edges, store.Edge{Source: relPath, Target: nodeID, Kind: "contains"})
			chunks = append(chunks, store.SectionChunk{
				NodeID:      nodeID,
				FilePath:    relPath,
				StartLine:   pageNum,
				EndLine:     pageNum,
				ContentHash: hash,
				SectionHash: sectionHash(bounded),
				HeadingPath: "Page " + strconv.Itoa(pageNum),
				Text:        bounded,
			})
		}
	}

	// --- Metadata tuples ---
	var metaTuples []store.MetadataTuple
	if docTitle != "" {
		metaTuples = append(metaTuples, store.MetadataTuple{Key: "pdf.title", Value: docTitle, ValueType: "string", Source: "pdf_info"})
	}
	if docAuthor != "" {
		metaTuples = append(metaTuples, store.MetadataTuple{Key: "pdf.author", Value: docAuthor, ValueType: "string", Source: "pdf_info"})
	}
	if docSubject != "" {
		metaTuples = append(metaTuples, store.MetadataTuple{Key: "pdf.subject", Value: docSubject, ValueType: "string", Source: "pdf_info"})
	}
	if docKeywords != "" {
		metaTuples = append(metaTuples, store.MetadataTuple{Key: "pdf.keywords", Value: docKeywords, ValueType: "string", Source: "pdf_info"})
	}
	if docCreationDate != "" {
		metaTuples = append(metaTuples, store.MetadataTuple{Key: "pdf.creation_date", Value: docCreationDate, ValueType: "string", Source: "pdf_info"})
	}
	if isScanned {
		metaTuples = append(metaTuples, store.MetadataTuple{Key: "warning", Value: "image-only-pdf", ValueType: "string", Source: "extractor"})
	}

	docNode := store.Node{
		ID:          relPath,
		Kind:        "document",
		Name:        docName,
		FilePath:    relPath,
		BodyExcerpt: bodyExcerpt,
		UpdatedAt:   now,
	}

	fileInfo := store.FileInfo{
		Path:        relPath,
		ContentHash: hash,
		NodeCount:   1 + len(pageNodes),
	}

	return &parser.ParseResult{
		DocNode:        docNode,
		Headings:       pageNodes,
		Edges:          edges,
		FileInfo:       fileInfo,
		SectionChunks:  chunks,
		MetadataTuples: metaTuples,
		RawLinks:       rawLinks,
	}, nil
}

// extractPageURIs walks a page's annotation array looking for /URI actions.
// Parse errors are silently ignored per spec.
func extractPageURIs(page pdf.Page, relPath string, pageNum int) []parser.RawLink {
	var links []parser.RawLink
	annots := page.V.Key("Annots")
	if annots.IsNull() {
		return links
	}
	for i := 0; i < annots.Len(); i++ {
		annot := annots.Index(i)
		if annot.IsNull() {
			continue
		}
		action := annot.Key("A")
		if action.IsNull() {
			continue
		}
		if action.Key("S").Name() != "URI" {
			continue
		}
		uri := action.Key("URI").Text()
		if uri == "" {
			uri = action.Key("URI").RawString()
		}
		if uri == "" {
			continue
		}
		nodeID := relPath + "#page-" + strconv.Itoa(pageNum)
		links = append(links, parser.RawLink{
			Text:       uri,
			Target:     uri,
			Kind:       "external",
			Line:       pageNum,
			FromNodeID: nodeID,
		})
	}
	return links
}

// sectionHash returns a SHA-256 hex string of the given section text.
func sectionHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h)
}

