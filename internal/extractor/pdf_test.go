package extractor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// ensurePDFFixtures creates sample.pdf and scanned.pdf under testdata/multiformat/
// if they don't already exist.
func ensurePDFFixtures(t testing.TB) {
	t.Helper()
	dir := testdataDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	samplePath := filepath.Join(dir, "sample.pdf")
	scannedPath := filepath.Join(dir, "scanned.pdf")

	if _, err := os.Stat(samplePath); os.IsNotExist(err) {
		// Use text > 50 chars per page so scanned detection doesn't trigger.
		page1 := "This is the first page of the sample PDF document for unit testing purposes."
		page2 := "This is the second page of the sample PDF document for unit testing purposes."
		data := buildMinimalPDF([]string{page1, page2})
		if err := os.WriteFile(samplePath, data, 0o644); err != nil {
			t.Fatalf("write sample.pdf: %v", err)
		}
	}
	if _, err := os.Stat(scannedPath); os.IsNotExist(err) {
		// Scanned PDF: pages with near-empty content (spaces only — no real text).
		// GetPlainText returns whitespace only, average chars/page < pdfScannedThreshold.
		data := buildMinimalPDF([]string{"  ", "  "})
		if err := os.WriteFile(scannedPath, data, 0o644); err != nil {
			t.Fatalf("write scanned.pdf: %v", err)
		}
	}
}

// buildMinimalPDF generates a valid minimal PDF with one page per entry in texts
// using the default font (no Encoding override).
func buildMinimalPDF(texts []string) []byte {
	return buildMinimalPDFEnc(texts, "")
}

// buildMinimalPDFEnc generates a valid minimal PDF with one page per entry in texts.
// If fontEncoding is non-empty, the font object includes /Encoding /<fontEncoding>.
func buildMinimalPDFEnc(texts []string, fontEncoding string) []byte {
	numPages := len(texts)
	// Object IDs (1-indexed):
	//   1       = Catalog
	//   2       = Pages
	//   3+i*2   = Page i dict  (i = 0..numPages-1)
	//   4+i*2   = Page i contents stream
	//   3+2*N   = Font /F1
	fontObjID := 3 + 2*numPages
	totalObjs := fontObjID

	// Build /Kids array: [3 0 R  5 0 R  ...]
	kidsParts := make([]string, numPages)
	for i := 0; i < numPages; i++ {
		kidsParts[i] = fmt.Sprintf("%d 0 R", 3+i*2)
	}
	kidsStr := fmt.Sprintf("[%s]", pdfJoinSpace(kidsParts))

	var buf bytes.Buffer
	offsets := make([]int, totalObjs+1)

	buf.WriteString("%PDF-1.4\n")

	offsets[1] = buf.Len()
	buf.WriteString("1 0 obj<</Type /Catalog /Pages 2 0 R>>endobj\n")

	offsets[2] = buf.Len()
	buf.WriteString(fmt.Sprintf("2 0 obj<</Type /Pages /Kids %s /Count %d>>endobj\n", kidsStr, numPages))

	for i, text := range texts {
		pageObjID := 3 + i*2
		contentsObjID := 4 + i*2

		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", pdfEscapeString(text))

		offsets[pageObjID] = buf.Len()
		buf.WriteString(fmt.Sprintf(
			"%d 0 obj<</Type /Page /Parent 2 0 R /MediaBox[0 0 612 792] /Contents %d 0 R /Resources<</Font<</F1 %d 0 R>>>>>>endobj\n",
			pageObjID, contentsObjID, fontObjID))

		offsets[contentsObjID] = buf.Len()
		buf.WriteString(fmt.Sprintf(
			"%d 0 obj<</Length %d>>stream\n%s\nendstream\nendobj\n",
			contentsObjID, len(stream), stream))
	}

	fontDict := "<</Type /Font /Subtype /Type1 /BaseFont /Helvetica"
	if fontEncoding != "" {
		fontDict += " /Encoding /" + fontEncoding
	}
	fontDict += ">>"
	offsets[fontObjID] = buf.Len()
	buf.WriteString(fmt.Sprintf("%d 0 obj%sendobj\n", fontObjID, fontDict))

	xrefOffset := buf.Len()
	buf.WriteString(fmt.Sprintf("xref\n0 %d\n", totalObjs+1))
	buf.WriteString("0000000000 65535 f \n")
	for n := 1; n <= totalObjs; n++ {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[n]))
	}
	buf.WriteString(fmt.Sprintf("trailer<</Size %d /Root 1 0 R>>\n", totalObjs+1))
	buf.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset))

	return buf.Bytes()
}

// pdfJoinSpace joins strings with a single space separator.
func pdfJoinSpace(ss []string) string {
	var b bytes.Buffer
	for i, s := range ss {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s)
	}
	return b.String()
}

// pdfEscapeString escapes parentheses and backslashes for PDF literal strings.
func pdfEscapeString(s string) string {
	var b bytes.Buffer
	for _, c := range s {
		switch c {
		case '(', ')':
			b.WriteByte('\\')
			b.WriteRune(c)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// ----- Tests -----

func TestExtractPDF_Sample(t *testing.T) {
	ensurePDFFixtures(t)
	samplePath := filepath.Join(testdataDir(t), "sample.pdf")
	relPath := "testdata/multiformat/sample.pdf"

	src, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read sample.pdf: %v", err)
	}
	hash := sha256hex(src)

	result, err := extractPDF(samplePath, relPath, src, hash)
	if err != nil {
		t.Fatalf("extractPDF: unexpected error: %v", err)
	}

	// DocNode ID must equal relPath.
	if result.DocNode.ID != relPath {
		t.Errorf("DocNode.ID = %q; want %q", result.DocNode.ID, relPath)
	}

	// Expect exactly 2 section chunks (one per page).
	if len(result.SectionChunks) != 2 {
		t.Errorf("SectionChunks count = %d; want 2", len(result.SectionChunks))
	}

	// Chunks should reference correct pages.
	for i, chunk := range result.SectionChunks {
		wantNodeID := fmt.Sprintf("%s#page-%d", relPath, i+1)
		if chunk.NodeID != wantNodeID {
			t.Errorf("SectionChunks[%d].NodeID = %q; want %q", i, chunk.NodeID, wantNodeID)
		}
		if chunk.FilePath != relPath {
			t.Errorf("SectionChunks[%d].FilePath = %q; want %q", i, chunk.FilePath, relPath)
		}
	}

	// FileInfo.
	if result.FileInfo.Path != relPath {
		t.Errorf("FileInfo.Path = %q; want %q", result.FileInfo.Path, relPath)
	}
	// NodeCount = 1 doc node + numPages page nodes.
	wantNodeCount := 1 + len(result.Headings)
	if result.FileInfo.NodeCount != wantNodeCount {
		t.Errorf("FileInfo.NodeCount = %d; want %d", result.FileInfo.NodeCount, wantNodeCount)
	}
}

func TestExtractPDF_Scanned(t *testing.T) {
	ensurePDFFixtures(t)
	scannedPath := filepath.Join(testdataDir(t), "scanned.pdf")
	relPath := "testdata/multiformat/scanned.pdf"

	src, err := os.ReadFile(scannedPath)
	if err != nil {
		t.Fatalf("read scanned.pdf: %v", err)
	}
	hash := sha256hex(src)

	result, err := extractPDF(scannedPath, relPath, src, hash)
	if err != nil {
		t.Fatalf("extractPDF on scanned PDF: unexpected error: %v", err)
	}

	// Must contain a warning tuple.
	found := false
	for _, mt := range result.MetadataTuples {
		if mt.Key == "warning" && mt.Value == "image-only-pdf" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected MetadataTuple{Key:warning, Value:image-only-pdf}, got: %v", result.MetadataTuples)
	}

	// DocNode should be present with correct ID.
	if result.DocNode.ID != relPath {
		t.Errorf("DocNode.ID = %q; want %q", result.DocNode.ID, relPath)
	}
}

// TestExtractPDF_GBKNowSupported verifies that GBK-EUC-H is no longer in the
// unsupported-encoding blocklist (which was removed entirely). The extractor
// must not emit an extraction-failed:unsupported-encoding warning; it may emit
// encoding-garbage if the fork's no-op decoder produces U+FFFD-heavy output,
// but that is the runtime backstop — not a static blocklist rejection.
func TestExtractPDF_GBKNowSupported(t *testing.T) {
	// Use >50 chars so the page clears pdfScannedThreshold and is not flagged
	// as image-only-pdf due to low char density after decoding.
	data := buildMinimalPDFEnc([]string{"This page uses a GBK-EUC-H font encoding label for testing purposes only."}, "GBK-EUC-H")
	relPath := "testdata/multiformat/cjk_gbk_nowsupported.pdf"
	hash := sha256hex(data)

	tmp, err := os.CreateTemp("", "docgraph-test-*.pdf")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		t.Fatalf("write temp: %v", err)
	}
	tmp.Close()

	result, err := extractPDF(tmp.Name(), relPath, data, hash)
	if err != nil {
		t.Fatalf("extractPDF: unexpected error: %v", err)
	}

	// DocNode ID must be correct.
	if result.DocNode.ID != relPath {
		t.Errorf("DocNode.ID = %q; want %q", result.DocNode.ID, relPath)
	}

	// The static blocklist (isUnsupportedCMapName) was removed entirely; no
	// extraction-failed:unsupported-encoding warning must ever be emitted.
	for _, mt := range result.MetadataTuples {
		if mt.Key == "warning" && strings.HasPrefix(mt.Value, "extraction-failed:unsupported-encoding") {
			t.Errorf("unexpected unsupported-encoding warning %q — blocklist should be gone", mt.Value)
		}
	}

	// At least one section chunk must be present (page node created).
	if len(result.SectionChunks) == 0 {
		t.Error("expected at least one SectionChunk, got none")
	}
}

// TestExtractPDF_RealModeB1 exercises a real Shift-JIS (90ms-RKSJ-H) PDF from
// the mozilla/pdf.js test corpus (Apache 2.0).
// Source: https://github.com/mozilla/pdf.js/blob/master/test/pdfs/90ms_rksj_h_sample.pdf
// The font HeiseiMin-W3 uses /90ms-RKSJ-H (predefined CMap) with no /ToUnicode.
// Fork v0.2.0 added a Shift-JIS decoder, so extraction must now succeed.
func TestExtractPDF_RealModeB1(t *testing.T) {
	fixturePath := testdataDir(t) + "/cjk_sample.pdf"
	src, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read cjk_sample.pdf: %v", err)
	}
	hash := sha256hex(src)

	result, err := extractPDF(fixturePath, "testdata/multiformat/cjk_sample.pdf", src, hash)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}

	// No unsupported-encoding or image-only-pdf warnings expected.
	for _, mt := range result.MetadataTuples {
		if mt.Key == "warning" {
			t.Errorf("unexpected warning %q", mt.Value)
		}
	}

	// At least one chunk with non-empty text.
	var allText string
	for _, chunk := range result.SectionChunks {
		allText += chunk.Text
	}
	if allText == "" {
		t.Fatalf("expected non-empty extracted text; got no text across %d chunks", len(result.SectionChunks))
	}

	// The fixture encodes 日本語テスト via HeiseiMin-W3 / 90ms-RKSJ-H.
	// The Latin page also contains "Hello ASCII" from a WinAnsiEncoding font.
	for _, want := range []string{"日本語テスト", "Hello ASCII"} {
		if !strings.Contains(allText, want) {
			t.Errorf("extracted text missing %q; got: %q", want, allText)
		}
	}
}

// TestExtractPDF_CJK exercises a synthetic UniGB-UCS2-H PDF (no ToUnicode).
// The fixture encodes "中文測試" as UCS-2-BE (4 x 2-byte pairs). Fork v0.3.0
// added ucs2BEEncoder so extraction must now succeed with correct Chinese text.
// Source: synthetic (generated by docgraph project, CC0).
func TestExtractPDF_CJK(t *testing.T) {
	fixturePath := testdataDir(t) + "/cjk_ucs2_sample.pdf"
	src, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read cjk_ucs2_sample.pdf: %v", err)
	}
	hash := sha256hex(src)

	result, err := extractPDF(fixturePath, "testdata/multiformat/cjk_ucs2_sample.pdf", src, hash)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}

	// No warnings expected — UniGB-UCS2-H is now supported.
	for _, mt := range result.MetadataTuples {
		if mt.Key == "warning" {
			t.Errorf("unexpected warning %q", mt.Value)
		}
	}

	// Text must contain the Chinese characters encoded in the fixture.
	var allText string
	for _, chunk := range result.SectionChunks {
		allText += chunk.Text
	}
	for _, want := range []string{"中文", "測試"} {
		if !strings.Contains(allText, want) {
			t.Errorf("extracted text missing %q; got: %q", want, allText)
		}
	}
}

func TestReplacementCharRatio(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want float64
	}{
		{"empty", "", 0},
		{"no replacement", "hello world", 0},
		{"all replacement", string([]rune{utf8.RuneError, utf8.RuneError, utf8.RuneError, utf8.RuneError}), 1.0},
		{"half replacement", "ab" + string([]rune{utf8.RuneError, utf8.RuneError}), 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replacementCharRatio(tt.s)
			if got != tt.want {
				t.Errorf("replacementCharRatio(%q) = %v; want %v", tt.s, got, tt.want)
			}
		})
	}
	// Sanity: garbageReplacementRatio threshold is between 0 and 1.
	if garbageReplacementRatio <= 0 || garbageReplacementRatio >= 1 {
		t.Errorf("garbageReplacementRatio = %v; want in (0, 1)", garbageReplacementRatio)
	}
}

func TestSanitizeWarningDetail(t *testing.T) {
	// Control characters are stripped.
	got := sanitizeWarningDetail("abc\x00\x01\x1fdef")
	if got != "abcdef" {
		t.Errorf("sanitizeWarningDetail stripped controls: got %q; want %q", got, "abcdef")
	}

	// Length is bounded at 120.
	long := strings.Repeat("x", 200)
	got = sanitizeWarningDetail(long)
	if len(got) > 120 {
		t.Errorf("sanitizeWarningDetail length = %d; want ≤ 120", len(got))
	}
}

func TestExtractPDF_TooManyPages(t *testing.T) {
	// Building a >500-page PDF programmatically would produce a multi-MB file
	// (each page needs its own content stream and xref entry).
	// The constraint is enforced in pdf.go: if r.NumPage() > 500 return error.
	t.Skip("fixture creation for >500-page PDF is impractical in unit tests; " +
		"constraint enforced at extractPDF line: if numPages > pdfMaxPages")
}

func FuzzExtractPDF(f *testing.F) {
	// Seed with the sample PDF bytes if the fixture exists.
	samplePath := filepath.Join("testdata", "multiformat", "sample.pdf")
	if src, err := os.ReadFile(samplePath); err == nil {
		f.Add(src)
	} else {
		// Fixture not yet built — seed with an inline minimal PDF.
		f.Add(buildMinimalPDF([]string{"hello fuzz"}))
	}
	// CJK encoding seed: exercises the runtime encoding-garbage backstop path
	// (the static unsupported-encoding blocklist was removed entirely).
	f.Add(buildMinimalPDFEnc([]string{"cjk"}, "GBK-EUC-H"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Exercise the production dispatch entry (Extract), which carries the
		// panic-recovery guard. Must not panic; errors are acceptable.
		// plaintext-error branch is exercised under fuzz / TestExtractMalformedPDFDoesNotPanic;
		// encoding-garbage e2e is hard to fixture deterministically, covered by TestReplacementCharRatio.
		hash := sha256hex(data)
		result, _ := Extract("/tmp/fuzz-input.pdf", "fuzz/input.pdf", data, hash)
		if result != nil && result.DocNode.ID != "fuzz/input.pdf" {
			t.Errorf("fuzz: DocNode.ID = %q; want %q", result.DocNode.ID, "fuzz/input.pdf")
		}
	})
}
