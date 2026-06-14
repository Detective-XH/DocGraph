package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/docformat"
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

type ScanOptions struct {
	NoGitignore bool
}

func ScanDir(root string) ([]FileEntry, error) {
	return ScanDirOpts(root, ScanOptions{})
}

// loadRootIgnores loads the root-level .gitignore and .docgraphignore into a
// slice of matchers. It is shared by ScanDirOpts and NewIgnoreMatcher so the
// two functions apply identical root-ignore rules.
func loadRootIgnores(root string, opts ScanOptions) []*gitignore {
	var ignores []*gitignore
	if !opts.NoGitignore {
		if gi := loadGitignore(filepath.Join(root, ".gitignore")); gi != nil {
			ignores = append(ignores, gi)
		}
	}
	if gi := loadGitignore(filepath.Join(root, ".docgraphignore")); gi != nil {
		ignores = append(ignores, gi)
	}
	return ignores
}

// scanHandleDir handles a directory entry inside the ScanDirOpts WalkDir
// callback. It enforces the security-relevant symlink skip, worktree skip,
// skipDirs skip, and loads any nested ignore files. The ignores pointer must
// not be nil; nested ignores are appended through it so the outer walk sees
// them for subsequent file entries.
func scanHandleDir(root, path string, d fs.DirEntry, opts ScanOptions, ignores *[]*gitignore) error {
	if skipDirs[d.Name()] {
		return filepath.SkipDir
	}
	// Security: never follow symlinked directories — they could escape the root.
	if d.Type()&os.ModeSymlink != 0 {
		return filepath.SkipDir
	}
	dirRel, _ := filepath.Rel(root, path)
	// Skip agent git worktrees — they are full repo copies that would
	// index duplicate documents and pollute search/similarity results.
	if dirRel == filepath.Join(".claude", "worktrees") {
		return filepath.SkipDir
	}
	if !opts.NoGitignore {
		if nested := loadGitignore(filepath.Join(path, ".gitignore")); nested != nil {
			nested.baseDir = dirRel
			*ignores = append(*ignores, nested)
		}
	}
	if nested := loadGitignore(filepath.Join(path, ".docgraphignore")); nested != nil {
		nested.baseDir = dirRel
		*ignores = append(*ignores, nested)
	}
	return nil
}

func ScanDirOpts(root string, opts ScanOptions) ([]FileEntry, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	ignores := loadRootIgnores(root, opts)

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
			return scanHandleDir(root, path, d, opts, &ignores)
		}

		// Security: never follow symlinked files.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !d.Type().IsRegular() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !docformat.SupportedExt(ext) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		if info.Size() > docformat.MaxFileSizeByExt[ext] {
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

// NewIgnoreMatcher returns a predicate reporting whether a project-relative path
// would be EXCLUDED by the active ignore rules — the same rules ScanDirOpts
// applies: .docgraphignore always, and .gitignore unless opts.NoGitignore, at the
// root and in every nested directory. It collects the ignore files once (a cheap
// walk that reads only ignore files, skipping the usual vendored/VCS dirs); the
// returned closure then classifies any number of paths.
//
// This is the safe signal for the serve-time delete-reconcile: a file that is
// still on disk but now matches an ignore rule is unambiguously meant to be
// excluded, so pruning it is correct. That is strictly narrower than raw
// scan-set membership (which also drops too-big / unreadable / transiently-absent
// files) and so cannot mass-delete the index when a scan momentarily returns few
// or no files.
// discoverNestedIgnores walks subdirectories of root to collect nested
// .gitignore and .docgraphignore files into ignores. Root-level files must
// already be loaded (this skips dirRel == "."). The same security guards as
// scanHandleDir apply (skipDirs, symlinks, worktrees).
func discoverNestedIgnores(root string, opts ScanOptions, ignores *[]*gitignore) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		// Security: never follow symlinked directories.
		if d.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		dirRel, _ := filepath.Rel(root, path)
		if dirRel == filepath.Join(".claude", "worktrees") {
			return filepath.SkipDir
		}
		if dirRel == "." {
			return nil // root ignore files already loaded above (baseDir "")
		}
		if !opts.NoGitignore {
			if nested := loadGitignore(filepath.Join(path, ".gitignore")); nested != nil {
				nested.baseDir = dirRel
				*ignores = append(*ignores, nested)
			}
		}
		if nested := loadGitignore(filepath.Join(path, ".docgraphignore")); nested != nil {
			nested.baseDir = dirRel
			*ignores = append(*ignores, nested)
		}
		return nil
	})
}

func NewIgnoreMatcher(root string, opts ScanOptions) (func(relPath string) bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	ignores := loadRootIgnores(root, opts)

	if err := discoverNestedIgnores(root, opts, &ignores); err != nil {
		return nil, err
	}

	return func(relPath string) bool {
		for _, gi := range ignores {
			if gi.matches(relPath) {
				return true
			}
		}
		return false
	}, nil
}

// ---------------------------------------------------------------------------
// Minimal stdlib .gitignore matcher (replaces github.com/sabhiram/go-gitignore)
// ---------------------------------------------------------------------------

type gitignore struct {
	// baseDir is the directory containing this .gitignore file, expressed as a
	// path relative to the scan root (empty string = root). Patterns apply only
	// to files whose relPath starts with baseDir.
	baseDir  string
	patterns []string
	negated  []bool
}

func loadGitignore(path string) *gitignore {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	gi := &gitignore{}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		neg := false
		if line[0] == '!' {
			neg = true
			line = line[1:]
		}
		// Strip leading "/" — gitignore uses it to anchor a pattern to the
		// directory containing the .gitignore file, which baseDir already handles.
		line = strings.TrimPrefix(line, "/")
		gi.patterns = append(gi.patterns, line)
		gi.negated = append(gi.negated, neg)
	}
	return gi
}

func (gi *gitignore) matches(relPath string) bool {
	if gi == nil {
		return false
	}
	// Patterns in a nested .gitignore only apply to files within that directory.
	if gi.baseDir != "" {
		prefix := gi.baseDir + string(filepath.Separator)
		if !strings.HasPrefix(relPath, prefix) {
			return false
		}
		// Strip the baseDir prefix so patterns match relative to their own dir.
		relPath = relPath[len(prefix):]
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
