package callout

import (
	"path/filepath"
	"sort"
	"strings"
)

var sensitiveKeywords = []string{
	"private", "personal", "confidential", "secret",
	"sensitive", "draft", "restricted", "classified",
}

// SensitiveFlag marks a folder path that contains at least one sensitive keyword component.
type SensitiveFlag struct {
	Path      string // relative folder path
	FileCount int
}

// FlagSensitivePaths returns entries where any path component contains a sensitive keyword
// (case-insensitive substring match within each component).
func FlagSensitivePaths(paths []string) []SensitiveFlag {
	folderCounts := make(map[string]int)
	for _, p := range paths {
		dir := filepath.ToSlash(filepath.Dir(p))
		if dir == "." {
			dir = ""
		}
		flagged := false
		for part := range strings.SplitSeq(filepath.ToSlash(p), "/") {
			if flagged {
				break
			}
			lower := strings.ToLower(part)
			for _, kw := range sensitiveKeywords {
				if strings.Contains(lower, kw) {
					folderCounts[dir]++
					flagged = true
					break
				}
			}
		}
	}
	result := make([]SensitiveFlag, 0, len(folderCounts))
	for path, count := range folderCounts {
		result = append(result, SensitiveFlag{Path: path, FileCount: count})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}

// IsAllSensitive returns true when every path has at least one sensitive keyword component.
func IsAllSensitive(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !pathHasSensitiveComponent(p) {
			return false
		}
	}
	return true
}

func pathHasSensitiveComponent(p string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(p), "/") {
		lower := strings.ToLower(part)
		for _, kw := range sensitiveKeywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}
