package docformat

// SupportedExt reports whether the lower-cased file extension is handled by
// DocGraph's indexing pipeline.
func SupportedExt(ext string) bool {
	_, ok := MaxFileSizeByExt[ext]
	return ok
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
}
