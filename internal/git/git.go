package git

import (
	"bufio"
	"bytes"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// IsRepo reports whether root is inside a git work tree. It runs
// `git rev-parse --is-inside-work-tree` once, letting callers short-circuit
// per-file CollectHistory forks when the tree is not version-controlled
// (or git is not installed). Returns false on any error.
func IsRepo(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree") // #nosec G204 -- literal "git" binary with fixed/structured args; no untrusted input
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

// forkSem is a process-wide budget that bounds the number of concurrent
// `git log --follow` child processes to NumCPU across ALL callers and
// goroutines. CollectHistories fans the per-file forks across its `workers`
// goroutines, and the multi-project workspace IndexAll fans CollectHistories
// across NumCPU projects at once; without a shared cap those two layers would
// multiply to NumCPU² concurrent git children (196 on a 14-core box, 4096 on
// 64). Under that fork/fd pressure `cmd.Output()` can fail with EAGAIN/ENOMEM,
// which CollectHistory swallows into a silently-wrong zero-value FileHistory —
// a correctness hazard, not just a slowdown. Acquiring here, around the fork
// itself, caps the global concurrent-git-child count by construction no matter
// how many layers fan out. A caller that fans a single layer (the per-store
// index flush passes NumCPU workers) fills the budget exactly and is unchanged.
var forkSem = make(chan struct{}, runtime.NumCPU())

// CollectHistory runs git log to gather change history for relPath within gitRoot.
// Returns a zero-value FileHistory (CommitCount == 0) on any error: git not installed,
// directory not a git repo, or file untracked.
func CollectHistory(gitRoot, relPath string) FileHistory {
	cmd := exec.Command("git", "-C", gitRoot, "log", "--follow", // #nosec G204 -- literal "git" binary with fixed/structured args; no untrusted input
		"--format=%at|%ae|%s", "--", relPath)
	// Bound concurrent git children to NumCPU process-wide (see forkSem). Held
	// only around the fork: the in-process parsing below runs off-budget so the
	// freed slot is reused immediately by another waiting collector.
	forkSem <- struct{}{}
	out, err := cmd.Output()
	<-forkSem
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
// Globally fork-bounded: every git child this spawns is gated by the
// package-level forkSem (cap NumCPU), so total concurrent `git log` children
// stay ≤ NumCPU even when multiple callers fan out at once. Both call sites
// rely on this — the single-store index flush (one CollectHistories at
// workers=NumCPU) and the multi-project workspace IndexAll (one CollectHistories
// per project, with NumCPU projects in flight). The per-project `workers` here
// can therefore be NumCPU regardless of how many projects run concurrently: it
// only sets how wide each project *requests*, never how many forks actually run
// (forkSem decides that). Blocked goroutines waiting on the budget are cheap;
// only ≤ NumCPU forks are ever live.
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
