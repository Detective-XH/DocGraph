package docformat

import (
	"fmt"
	"io"
	"os"
)

// SupportedExt reports whether the lower-cased file extension is handled by
// DocGraph's indexing pipeline.
func SupportedExt(ext string) bool {
	_, ok := MaxFileSizeByExt[ext]
	return ok
}

// ReadFileCapped reads path fully into memory but returns an error if the file
// exceeds limit bytes. It bounds the live read with an io.LimitReader instead
// of trusting a scan-time size check, which is separated from the read by a
// TOCTOU window (and absent entirely on live, MCP-triggered reads). A non-
// positive limit reads the file with no cap.
func ReadFileCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if limit <= 0 {
		return io.ReadAll(f)
	}

	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file %q exceeds read cap of %d bytes", path, limit)
	}
	return data, nil
}

// MaxFileSizeByExt maps each supported extension to its physical-file size
// cap in bytes.  Markdown keeps the historic 1 MB limit; binary formats allow
// larger physical files because compression ratios and per-format security
// guards (zip-bomb, page cap) are enforced inside the extractor.
var MaxFileSizeByExt = map[string]int64{
	".md":   1_048_576,  // 1 MB — historic limit
	".docx": 10_485_760, // 10 MB physical (uncompressed budget enforced by extractor)
	".html": 5_242_880,  // 5 MB
	".htm":  5_242_880,  // 5 MB
	".pdf":  52_428_800, // 50 MB physical

	// Code source files — indexed when the code_doc domain pack is enabled.
	".go":     1_048_576,
	".py":     1_048_576,
	".rb":     1_048_576,
	".js":     1_048_576,
	".jsx":    1_048_576,
	".ts":     1_048_576,
	".tsx":    1_048_576,
	".svelte": 1_048_576,
	".vue":    1_048_576,
	".rs":     1_048_576,
	".c":      1_048_576,
	".cc":     1_048_576,
	".h":      1_048_576,
	".cpp":    1_048_576,
	".cxx":    1_048_576,
	".hpp":    1_048_576,
	".hh":     1_048_576,
	".java":   1_048_576,
	".swift":  1_048_576,
	".cs":     1_048_576,
	".php":    1_048_576,
	".kt":     1_048_576,
	".kts":    1_048_576,
	".dart":   1_048_576,
	".lua":    1_048_576,
	".luau":   1_048_576,
	".pas":    1_048_576,
	".pp":     1_048_576,
	".sql":    1_048_576,
	".liquid": 1_048_576,
}
