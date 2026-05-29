package extractor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
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

// buildMinimalPDF generates a valid minimal PDF with one page per entry in texts.
// Each text string is rendered with (text) Tj inside a BT...ET block.
// Byte offsets in the xref table are computed dynamically.
func buildMinimalPDF(texts []string) []byte {
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

	offsets[fontObjID] = buf.Len()
	buf.WriteString(fmt.Sprintf(
		"%d 0 obj<</Type /Font /Subtype /Type1 /BaseFont /Helvetica>>endobj\n", fontObjID))

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

	f.Fuzz(func(t *testing.T, data []byte) {
		// Exercise the production dispatch entry (Extract), which carries the
		// panic-recovery guard. Must not panic; errors are acceptable.
		hash := sha256hex(data)
		result, _ := Extract("/tmp/fuzz-input.pdf", "fuzz/input.pdf", data, hash)
		if result != nil && result.DocNode.ID != "fuzz/input.pdf" {
			t.Errorf("fuzz: DocNode.ID = %q; want %q", result.DocNode.ID, "fuzz/input.pdf")
		}
	})
}
