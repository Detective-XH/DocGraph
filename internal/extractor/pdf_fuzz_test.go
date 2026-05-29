package extractor

import (
	"strings"
	"testing"
)

// minimalPDF is a hand-rolled, structurally minimal PDF used as a fuzz seed so
// the mutator starts from input the underlying parser will actually try to walk.
const minimalPDF = "%PDF-1.4\n" +
	"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
	"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
	"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]>>endobj\n" +
	"trailer<</Root 1 0 R>>\n%%EOF\n"

// malformedPDFSeeds are inputs that exercise the third-party PDF parser's
// failure paths (truncated header, broken xref, lying object counts). The
// extractor must convert any failure — including a panic inside the
// third-party library — into a returned error, never a process crash.
var malformedPDFSeeds = []string{
	"",
	"%PDF-1.4",
	"%PDF-1.4\n",
	"%PDF-1.7\ntrailer<</Root 1 0 R>>\n%%EOF",
	"%PDF-1.4\n1 0 obj<</Type/Pages/Count 9999>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF",
	"%PDF-1.4\n" + strings.Repeat("0 0 obj<<>>endobj\n", 64) + "trailer<</Root 1 0 R>>\n%%EOF",
	minimalPDF,
}

// TestExtractMalformedPDFDoesNotPanic is the regression guard for the panic
// recovery in Extract: feeding hostile/malformed PDF bytes must yield an error
// or a result, but must never panic out of the extractor.
func TestExtractMalformedPDFDoesNotPanic(t *testing.T) {
	for i, seed := range malformedPDFSeeds {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("seed %d: Extract panicked instead of returning an error: %v", i, r)
				}
			}()
			// Result is intentionally ignored; we only assert the absence of a panic.
			_, _ = Extract("/tmp/fuzz.pdf", "fuzz.pdf", []byte(seed), "deadbeef")
		}()
	}
}
