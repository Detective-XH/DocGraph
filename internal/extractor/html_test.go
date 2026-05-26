package extractor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractHTML_Sample(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "multiformat", "sample.html"))
	if err != nil {
		t.Fatalf("read sample.html: %v", err)
	}

	res, err := extractHTML("/abs/sample.html", "sample.html", src, "testhash")
	if err != nil {
		t.Fatalf("extractHTML: %v", err)
	}

	// DocNode title
	if res.DocNode.Name != "Sample Page" {
		t.Errorf("DocNode.Name = %q, want %q", res.DocNode.Name, "Sample Page")
	}

	// 2 heading nodes
	if len(res.Headings) != 2 {
		t.Errorf("len(Headings) = %d, want 2", len(res.Headings))
	}

	// MetadataTuples contains "description"
	foundDesc := false
	for _, mt := range res.MetadataTuples {
		if mt.Key == "description" && mt.Source == "html_meta" {
			foundDesc = true
		}
	}
	if !foundDesc {
		t.Errorf("MetadataTuples missing key=description with source=html_meta; got %+v", res.MetadataTuples)
	}

	// RawLinks contains "https://example.com"
	foundLink := false
	for _, rl := range res.RawLinks {
		if rl.Target == "https://example.com" {
			foundLink = true
		}
	}
	if !foundLink {
		t.Errorf("RawLinks missing https://example.com; got %+v", res.RawLinks)
	}

	// script text NOT in body excerpt
	if res.DocNode.BodyExcerpt != "" {
		scriptText := "alert('should be stripped');"
		if contains(res.DocNode.BodyExcerpt, scriptText) {
			t.Errorf("BodyExcerpt contains script text that should be stripped")
		}
	}
}

func TestExtractHTML_NoTitle(t *testing.T) {
	src := []byte("<h1>Hello</h1>")
	res, err := extractHTML("/abs/hello.html", "hello.html", src, "h")
	if err != nil {
		t.Fatalf("extractHTML: %v", err)
	}
	if res.DocNode.Name != "hello.html" {
		t.Errorf("DocNode.Name = %q, want %q", res.DocNode.Name, "hello.html")
	}
}

func TestExtractHTML_MetaOG(t *testing.T) {
	src := []byte(`<html><head><meta property="og:title" content="OG Title"></head></html>`)
	res, err := extractHTML("/abs/og.html", "og.html", src, "h")
	if err != nil {
		t.Fatalf("extractHTML: %v", err)
	}
	found := false
	for _, mt := range res.MetadataTuples {
		if mt.Key == "og:title" && mt.Source == "html_meta" {
			found = true
		}
	}
	if !found {
		t.Errorf("MetadataTuples missing key=og:title with source=html_meta; got %+v", res.MetadataTuples)
	}
}

func FuzzExtractHTML(f *testing.F) {
	seed, err := os.ReadFile(filepath.Join("..", "..", "testdata", "multiformat", "sample.html"))
	if err == nil {
		f.Add(seed)
	}
	f.Add([]byte("<html><head><title>T</title></head><body><h1>H</h1></body></html>"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = extractHTML("a.html", "a.html", data, "h")
	})
}

// contains is a simple substring check used in tests.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
