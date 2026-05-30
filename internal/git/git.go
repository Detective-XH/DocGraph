package git

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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

// CollectHistories collects a FileHistory for every relPath under gitRoot,
// fanning the per-file `git log --follow` forks across up to `workers`
// goroutines. On a large versioned corpus this is the dominant index cost:
// each fork is CPU-bound `--follow` rename detection that runs in a child
// process, so a serial loop pegs a single core while the rest idle (measured
// on a 64k-commit vscode clone, 383 doc files: ~304s wall serial vs ~32s with
// workers=NumCPU on 14 cores — 9.4×, file_history rows bit-identical to serial).
//
// CollectHistory is a pure per-file function with no shared state, so each
// goroutine writes its own disjoint results slot — no mutex, no batcher (unlike
// similarity.runPairwiseWorkers, whose edge writes must funnel through one
// SQLite writer). Rows are returned in the same order as relPaths so callers
// can keep their serial UpsertFileHistory loop unchanged. workers <= 1, an
// empty list, or a single path runs serially; workers is clamped to len.
//
// No global cap: each call spawns up to `workers` concurrent git children, so a
// caller that invokes CollectHistories from multiple goroutines must divide the
// budget — passing NumCPU from N concurrent callers oversubscribes to N×NumCPU
// forks. The only caller today is the serial per-store index flush, which passes
// NumCPU safely; the multi-project workspace path deliberately stays serial (it
// already parallelizes across projects, so a per-project fan-out would multiply).
func CollectHistories(gitRoot string, relPaths []string, workers int) []FileHistory {
	results := make([]FileHistory, len(relPaths))
	if workers > len(relPaths) {
		workers = len(relPaths)
	}
	if workers <= 1 {
		for i, p := range relPaths {
			results[i] = CollectHistory(gitRoot, p)
		}
		return results
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		// Round-robin rows (i += workers), mirroring runPairwiseWorkers, so no
		// goroutine owns a contiguous block that could skew under uneven
		// per-file history depth.
		go func(start int) {
			defer wg.Done()
			for i := start; i < len(relPaths); i += workers {
				results[i] = CollectHistory(gitRoot, relPaths[i])
			}
		}(w)
	}
	wg.Wait()
	return results
}
