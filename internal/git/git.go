package git

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// IsRepo reports whether root is inside a git work tree. It runs
// `git rev-parse --is-inside-work-tree` once, letting callers short-circuit
// per-file CollectHistory forks when the tree is not version-controlled
// (or git is not installed). Returns false on any error.
func IsRepo(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return string(bytes.TrimSpace(out)) == "true"
}

// FileHistory holds git commit statistics for a single file.
type FileHistory struct {
	Path          string
	CommitCount   int
	FirstCommitAt int64
	LastCommitAt  int64
	AuthorCount   int
	LastAuthor    string
	LastSubject   string
}

// CollectHistory runs git log to gather change history for relPath within gitRoot.
// Returns a zero-value FileHistory (CommitCount == 0) on any error: git not installed,
// directory not a git repo, or file untracked.
func CollectHistory(gitRoot, relPath string) FileHistory {
	cmd := exec.Command("git", "-C", gitRoot, "log", "--follow",
		"--format=%at|%ae|%s", "--", relPath)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return FileHistory{Path: relPath}
	}

	var timestamps []int64
	authors := make(map[string]struct{})
	var lastAuthor, lastSubject string
	first := true

	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		ts, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		timestamps = append(timestamps, ts)
		authors[parts[1]] = struct{}{}
		if first {
			lastAuthor = parts[1]
			lastSubject = parts[2]
			first = false
		}
	}

	if len(timestamps) == 0 {
		return FileHistory{Path: relPath}
	}

	// git log outputs newest-first, so timestamps[0] is latest, last element is oldest.
	return FileHistory{
		Path:          relPath,
		CommitCount:   len(timestamps),
		FirstCommitAt: timestamps[len(timestamps)-1],
		LastCommitAt:  timestamps[0],
		AuthorCount:   len(authors),
		LastAuthor:    lastAuthor,
		LastSubject:   lastSubject,
	}
}
