package extractor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Detective-XH/pdf"

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

func extractPDF(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	// Write src to a temp file since Detective-XH/pdf requires a file path.
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
	var metaTuples []store.MetadataTuple
	var unsupportedEncoding bool

	pageTexts := make([]string, numPages)
	seenBadEnc := make(map[string]bool)
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

		// Phase 0 (detection/triage): Detective-XH/pdf decodes fonts whose
		// Encoding is an unrecognised predefined CMap name (e.g. GBK-EUC-H)
		// via a no-op encoder — raw bytes pass through as U+FFFD garbage with
		// a NIL error. Detect such fonts by name and skip text extraction for
		// the page rather than indexing garbage.
		var text string
		if encName, bad := pageUnsupportedEncoding(pageFontNames, fonts); bad {
			unsupportedEncoding = true
			if !seenBadEnc[encName] {
				// One tuple per distinct CMap name per document — the honest
				// semantic is "this document uses an unsupported encoding", not
				// "N separate problems" (N = page count).
				seenBadEnc[encName] = true
				metaTuples = append(metaTuples, store.MetadataTuple{
					Key:       "warning",
					Value:     "extraction-failed:unsupported-encoding:" + encName,
					ValueType: "string",
					Source:    "extractor",
				})
			}
		} else {
			t, err := page.GetPlainText(fonts)
			if err != nil {
				// Previously discarded (text, _ :=); surface it so a parse/encoding
				// failure is visible. The error originates from a third-party parser
				// on untrusted bytes, so sanitize + bound it and tag it under a fixed
				// sub-key that cannot be confused with the structured detections above
				// (prevents metadata/prompt injection via a crafted PDF).
				metaTuples = append(metaTuples, store.MetadataTuple{
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
					metaTuples = append(metaTuples, store.MetadataTuple{
						Key:       "warning",
						Value:     "extraction-failed:encoding-garbage",
						ValueType: "string",
						Source:    "extractor",
					})
				}
				t = ""
			}
			text = t
		}
		pageTexts[n-1] = text
		totalChars += len(text)

		// Extract URI annotations from this page (encoding-independent).
		rawLinks = append(rawLinks, extractPageURIs(page, relPath, n)...)
	}

	// --- Scanned PDF detection ---
	avgChars := 0
	if numPages > 0 {
		avgChars = totalChars / numPages
	}
	// A document whose text we deliberately skipped due to an unsupported
	// encoding is NOT image-only; suppress the scanned heuristic so the
	// extraction-failed:unsupported-encoding warning is the single honest signal.
	isScanned := numPages > 0 && avgChars < pdfScannedThreshold && !unsupportedEncoding

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

// pageUnsupportedEncoding reports whether any of the page's named fonts declares
// a predefined CMap name that Detective-XH/pdf cannot decode (it falls through to
// a no-op encoder, yielding U+FFFD garbage with a nil error). Returns the first
// such CMap name; the returned name is always one of the constants matched by
// isUnsupportedCMapName, never attacker-controlled free text.
//
// Note: a positive result causes the whole page's text to be skipped, including
// text from any well-encoded (e.g. Latin) fonts on the same page. That is an
// acceptable Phase-0 triage tradeoff; per-font selective extraction is deferred
// to the Phase 2 fork.
func pageUnsupportedEncoding(fontNames []string, fonts map[string]*pdf.Font) (string, bool) {
	for _, name := range fontNames {
		f, ok := fonts[name]
		if !ok || f == nil {
			continue
		}
		enc := f.V.Key("Encoding")
		if enc.Kind() != pdf.Name {
			continue
		}
		if n := enc.Name(); isUnsupportedCMapName(n) {
			return n, true
		}
	}
	return "", false
}

// isUnsupportedCMapName reports whether a font Encoding name is a predefined
// CMap that Detective-XH/pdf does not implement. These predefined CJK CMaps
// decode to garbage via the library's no-op encoder; Phase 0 detects and skips them.
//
// Implemented (removed from this list as the fork adds decoders):
//   - 90ms-RKSJ-H/V, 90pv-RKSJ-H         → Shift-JIS via x/text (fork v0.2.0)
//   - UniGB/UniCNS/UniJIS/UniKS-UCS2-H/V  → UCS-2-BE direct decode (fork v0.3.0)
func isUnsupportedCMapName(name string) bool {
	switch name {
	case "GBK-EUC-H", "GBK-EUC-V",
		"ETen-B5-H", "ETen-B5-V",
		"KSCms-UHC-H", "KSCms-UHC-V":
		return true
	default:
		return false
	}
}

// replacementCharRatio returns the fraction of runes in s equal to the Unicode
// replacement character (U+FFFD). Detective-XH/pdf's no-op encoder emits U+FFFD
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
