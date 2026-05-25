package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type FileEntry struct {
	Path       string
	RelPath    string
	Size       int64
	ModifiedAt int64
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "dist": true,
	"build": true, ".codegraph": true, ".docgraph": true, ".next": true,
	".cache": true, "vendor": true, "__pycache__": true, ".obsidian": true,
}

const maxFileSize = 1_048_576

type ScanOptions struct {
	NoGitignore bool
}

func ScanDir(root string) ([]FileEntry, error) {
	return ScanDirOpts(root, ScanOptions{})
}

func ScanDirOpts(root string, opts ScanOptions) ([]FileEntry, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var ignores []*gitignore
	if !opts.NoGitignore {
		if gi := loadGitignore(filepath.Join(root, ".gitignore")); gi != nil {
			ignores = append(ignores, gi)
		}
	}
	if gi := loadGitignore(filepath.Join(root, ".docgraphignore")); gi != nil {
		ignores = append(ignores, gi)
	}

	matchesAny := func(relPath string) bool {
		for _, gi := range ignores {
			if gi.matches(relPath) {
				return true
			}
		}
		return false
	}

	var entries []FileEntry

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if d.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			if !opts.NoGitignore {
				if nested := loadGitignore(filepath.Join(path, ".gitignore")); nested != nil {
					ignores = append(ignores, nested)
				}
			}
			if nested := loadGitignore(filepath.Join(path, ".docgraphignore")); nested != nil {
				ignores = append(ignores, nested)
			}
			return nil
		}

		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !d.Type().IsRegular() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		if info.Size() > maxFileSize {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if matchesAny(relPath) {
			return nil
		}

		entries = append(entries, FileEntry{
			Path:       path,
			RelPath:    relPath,
			Size:       info.Size(),
			ModifiedAt: info.ModTime().Unix(),
		})
		return nil
	})

	return entries, err
}

// ---------------------------------------------------------------------------
// Minimal stdlib .gitignore matcher (replaces github.com/sabhiram/go-gitignore)
// ---------------------------------------------------------------------------

type gitignore struct {
	patterns []string
	negated  []bool
}

func loadGitignore(path string) *gitignore {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	gi := &gitignore{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		neg := false
		if line[0] == '!' {
			neg = true
			line = line[1:]
		}
		gi.patterns = append(gi.patterns, line)
		gi.negated = append(gi.negated, neg)
	}
	return gi
}

func (gi *gitignore) matches(relPath string) bool {
	if gi == nil {
		return false
	}
	matched := false
	for i, pattern := range gi.patterns {
		// Match against basename and full relative path
		base := filepath.Base(relPath)
		if ok, _ := filepath.Match(pattern, base); ok {
			matched = !gi.negated[i]
		}
		if ok, _ := filepath.Match(pattern, relPath); ok {
			matched = !gi.negated[i]
		}
		// Handle directory patterns (trailing /)
		dir := strings.TrimSuffix(pattern, "/")
		if dir != pattern {
			if strings.HasPrefix(relPath, dir+"/") || relPath == dir {
				matched = !gi.negated[i]
			}
		}
		// Handle patterns without slash = match anywhere
		if !strings.Contains(pattern, "/") {
			if strings.Contains(relPath, pattern) {
				matched = !gi.negated[i]
			}
		}
	}
	return matched
}
