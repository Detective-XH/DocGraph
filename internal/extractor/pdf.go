package extractor

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Detective-XH/gopdf" // repo/module renamed pdf → gopdf; package identifier is still `pdf`

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

const (
	pdfMaxPages             = 500
	pdfMaxTextLen           = 10 * 1024 // 10 KB per section chunk
	pdfExcerptLen           = 500       // 500 byte body excerpt cap
	pdfScannedThreshold     = 50        // avg chars/page below which we flag as image-only
	garbageReplacementRatio = 0.3       // U+FFFD rune fraction above which page text is treated as encoding garbage
)

// pdfPageResult holds the outputs of extractPDFPages.
type pdfPageResult struct {
	pageTexts    []string
	totalChars   int
	rawLinks     []parser.RawLink
	pageWarnings []store.MetadataTuple
}

// extractPDFPages iterates over every page: caches fonts, calls GetPlainText,
// applies the security gate (U+FFFD garbage detection), and collects URI links.
// Page-level warning tuples are returned separately so the orchestrator can
// place them before the Info-dict tuples (preserving metaTuples append order).
func extractPDFPages(r *pdf.Reader, relPath string, numPages int) pdfPageResult {
	fonts := make(map[string]*pdf.Font)
	pageTexts := make([]string, numPages)
	var rawLinks []parser.RawLink
	var pageWarnings []store.MetadataTuple
	var totalChars int

	for n := 1; n <= numPages; n++ {
		page := r.Page(n)
		// Cache fonts for performance.
		pageFontNames := page.Fonts()
		for _, name := range pageFontNames {
			if _, ok := fonts[name]; !ok {
				f := page.Font(name)
				fonts[name] = &f
			}
		}

		t, err := page.GetPlainText(fonts)
		if err != nil {
			// Previously discarded (text, _ :=); surface it so a parse/encoding
			// failure is visible. The error originates from a third-party parser
			// on untrusted bytes, so sanitize + bound it and tag it under a fixed
			// sub-key that cannot be confused with the structured detections above
			// (prevents metadata/prompt injection via a crafted PDF).
			pageWarnings = append(pageWarnings, store.MetadataTuple{
				Key:       "warning",
				Value:     "extraction-failed:plaintext-error:" + sanitizeWarningDetail(err.Error()),
				ValueType: "string",
				Source:    "extractor",
			})
		}
		if replacementCharRatio(t) >= garbageReplacementRatio {
			// nopEncoder garbage surfaces as U+FFFD runes, which are valid
			// UTF-8 — so utf8.ValidString cannot detect it. A high U+FFFD
			// density is the reliable signal that an unlisted CMap decoded to
			// replacement characters; discard rather than index the garbage.
			if err == nil {
				pageWarnings = append(pageWarnings, store.MetadataTuple{
					Key:       "warning",
					Value:     "extraction-failed:encoding-garbage",
					ValueType: "string",
					Source:    "extractor",
				})
			}
			t = ""
		}
		pageTexts[n-1] = t
		totalChars += len(t)

		// Extract URI annotations from this page (encoding-independent).
		rawLinks = append(rawLinks, extractPageURIs(page, relPath, n)...)
	}

	return pdfPageResult{
		pageTexts:    pageTexts,
		totalChars:   totalChars,
		rawLinks:     rawLinks,
		pageWarnings: pageWarnings,
	}
}

// extractPDFMeta reads the PDF Info dict and returns the display name and the
// info metadata tuples (title/author/subject/keywords/creation_date), each
// gated on non-empty, in the canonical append order.
func extractPDFMeta(r *pdf.Reader, relPath string) (docName string, infoTuples []store.MetadataTuple) {
	// --- Extract Info dict via the v0.6.0 Metadata API. r.Info() wraps
	// Trailer → Info (not Trailer → Root → Info) and parses dates per PDF
	// spec §14.3.3, so we no longer hand-walk the dictionary. ---
	info := r.Info()
	docTitle := strings.TrimSpace(info.Title())
	docAuthor := strings.TrimSpace(info.Author())
	docSubject := strings.TrimSpace(info.Subject())
	docKeywords := strings.TrimSpace(info.Keywords())
	// CreationDate() returns a parsed time.Time (zero on absent/unparseable).
	// Normalize to an RFC3339 UTC timestamp so the stored tuple is a clean,
	// sortable ISO date rather than the raw "D:YYYYMMDDHHmmSS±HH'mm'" string.
	var docCreationDate string
	if t := info.CreationDate(); !t.IsZero() {
		docCreationDate = t.UTC().Format(time.RFC3339)
	}

	// Determine display name: prefer title from info dict, else basename.
	docName = docTitle
	if docName == "" {
		docName = filepath.Base(relPath)
	}

	if docTitle != "" {
		infoTuples = append(infoTuples, store.MetadataTuple{Key: "pdf.title", Value: docTitle, ValueType: "string", Source: "pdf_info"})
	}
	if docAuthor != "" {
		infoTuples = append(infoTuples, store.MetadataTuple{Key: "pdf.author", Value: docAuthor, ValueType: "string", Source: "pdf_info"})
	}
	if docSubject != "" {
		infoTuples = append(infoTuples, store.MetadataTuple{Key: "pdf.subject", Value: docSubject, ValueType: "string", Source: "pdf_info"})
	}
	if docKeywords != "" {
		infoTuples = append(infoTuples, store.MetadataTuple{Key: "pdf.keywords", Value: docKeywords, ValueType: "string", Source: "pdf_info"})
	}
	if docCreationDate != "" {
		infoTuples = append(infoTuples, store.MetadataTuple{Key: "pdf.creation_date", Value: docCreationDate, ValueType: "string", Source: "pdf_info"})
	}
	return docName, infoTuples
}

// buildPDFNodes constructs page nodes, containment edges, and section chunks.
// now must be a single timestamp from the orchestrator so all nodes share it.
func buildPDFNodes(relPath, hash string, pageTexts []string, isScanned bool, now int64) (pageNodes []store.Node, edges []store.Edge, chunks []store.SectionChunk) {
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
		return pageNodes, edges, chunks
	}
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
	return pageNodes, edges, chunks
}

func extractPDF(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	_ = absPath
	r, err := pdf.OpenBytes(src)
	if err != nil {
		return nil, fmt.Errorf("extractPDF: open PDF: %w", err)
	}

	numPages := r.NumPage()
	if numPages > pdfMaxPages {
		return nil, fmt.Errorf("extractPDF: PDF has %d pages, exceeds cap of %d", numPages, pdfMaxPages)
	}

	docName, infoTuples := extractPDFMeta(r, relPath)
	pages := extractPDFPages(r, relPath, numPages)

	// --- Scanned PDF detection ---
	avgChars := 0
	if numPages > 0 {
		avgChars = pages.totalChars / numPages
	}
	isScanned := numPages > 0 && avgChars < pdfScannedThreshold

	// --- Body excerpt from page 1 text (500 byte cap) ---
	var bodyExcerpt string
	if len(pages.pageTexts) > 0 {
		t := strings.TrimSpace(pages.pageTexts[0])
		if len(t) > pdfExcerptLen {
			t = t[:pdfExcerptLen]
		}
		bodyExcerpt = t
	}

	// now is computed once so all nodes (page and doc) share a single timestamp.
	now := time.Now().Unix()
	pageNodes, edges, chunks := buildPDFNodes(relPath, hash, pages.pageTexts, isScanned, now)

	// --- Metadata tuples: page warnings first, then Info-dict tuples, then image-only ---
	metaTuples := pages.pageWarnings
	metaTuples = append(metaTuples, infoTuples...)
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
		RawLinks:       pages.rawLinks,
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

// replacementCharRatio returns the fraction of runes in s equal to the Unicode
// replacement character (U+FFFD). Detective-XH/gopdf's no-op encoder emits U+FFFD
// for every byte it cannot decode, and U+FFFD is itself valid UTF-8 — so a high
// ratio (not utf8.ValidString, which always passes) is the reliable signal of
// encoding garbage from an unlisted CMap. Returns 0 for the empty string.
func replacementCharRatio(s string) float64 {
	if s == "" {
		return 0
	}
	var total, bad int
	for _, r := range s {
		total++
		if r == utf8.RuneError {
			bad++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(bad) / float64(total)
}

// sanitizeWarningDetail strips control characters and the replacement rune from
// an untrusted parser error and bounds its length, so attacker-controlled error
// text from a malformed PDF cannot inject structure or bloat into stored
// metadata that an LLM later reads as authoritative.
func sanitizeWarningDetail(s string) string {
	const maxLen = 120
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= maxLen {
			break
		}
	}
	return b.String()
}

// sectionHash returns a SHA-256 hex string of the given section text.
func sectionHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h)
}
