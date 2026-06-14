package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Detective-XH/docgraph/internal/scanner"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/tools"
	"github.com/Detective-XH/docgraph/internal/watcher"
	"github.com/Detective-XH/docgraph/internal/workspace"
	mcp "github.com/mark3labs/mcp-go/server"
)

// anyProjectDBExists checks whether at least one subdirectory of wsRoot already
// has a .docgraph/docgraph.db. Used to decide warm-start vs cold-start.
func anyProjectDBExists(wsRoot string) bool {
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		if _, err := os.Stat(filepath.Join(wsRoot, e.Name(), ".docgraph", "docgraph.db")); err == nil {
			return true
		}
	}
	return false
}

func cmdServe(args []string) {
	fset := flag.NewFlagSet("serve", flag.ExitOnError)
	p := fset.String("path", "", "Project directory to index and serve")
	ws := fset.String("workspace", "", "Workspace root (index all child dirs as projects)")
	fset.BoolVar(&noGitignore, "no-gitignore", false, "Ignore .gitignore rules, index all .md files")
	fset.BoolVar(&noHistory, "no-history", false, "Skip git commit-history collection (file_history; on by default)")
	fset.Float64Var(&similarityThreshold, "threshold", 0, "Similarity threshold for similar_to edges (default 0.25)")
	enableEmbeddings := fset.Bool("enable-embeddings", false, "Register docgraph_embeddings (sends content to external LLM provider)")
	enableEnrichment := fset.Bool("enable-enrichment", false, "Register docgraph_enrichment (sends content to external LLM provider)")
	// Cap fsnotify watches per process to bound open fds: on macOS (kqueue) every
	// watched dir/file is one fd, so recursively watching a large workspace —
	// multiplied across every concurrent serve (one per MCP client) — can exhaust
	// the system file table. DOCGRAPH_MAX_WATCHES overrides the default; --max-watches
	// overrides both; 0 disables the cap.
	defMaxWatches := watcher.DefaultMaxWatches
	if v := os.Getenv("DOCGRAPH_MAX_WATCHES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			defMaxWatches = n
		} else {
			fmt.Fprintf(os.Stderr, "[serve] ignoring invalid DOCGRAPH_MAX_WATCHES=%q\n", v)
		}
	}
	maxWatches := fset.Int("max-watches", defMaxWatches, "Max directories+files to watch per process (0 = unlimited; bounds open file descriptors)")
	fset.Parse(args)

	regOpts := tools.RegisterOpts{
		EnableEmbeddings: *enableEmbeddings,
		EnableEnrichment: *enableEnrichment,
		NoGitignore:      noGitignore,
	}

	srv := mcp.NewMCPServer("docgraph", "0.1.0", mcp.WithInstructions(serverInstructions))

	var closer io.Closer
	if *ws != "" {
		closer = serveWorkspace(srv, *ws, *maxWatches, regOpts)
	} else {
		closer = serveSinglePath(srv, *p, *maxWatches, regOpts)
	}
	defer closer.Close()

	stdio := mcp.NewStdioServer(srv)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// serveWorkspace wires the --workspace arm: opens all child-project stores,
// registers workspace tools on srv, starts background indexing and the watcher.
// Returns a Closer that must be deferred by the caller (after stdio.Listen returns).
func serveWorkspace(srv *mcp.MCPServer, wsRoot string, maxWatches int, regOpts tools.RegisterOpts) io.Closer {
	warm := anyProjectDBExists(wsRoot)
	w, err := workspace.Open(wsRoot)
	if err != nil {
		log.Fatal(err)
	}
	w.NoGitignore = noGitignore
	w.NoHistory = noHistory
	w.SimilarityThreshold = similarityThreshold
	setIndexing := tools.RegisterWorkspaceWithOpts(srv, w, regOpts)
	doSync := func() {
		defer setIndexing(false)
		if err := w.IndexAll(); err != nil {
			log.Printf("[serve] IndexAll: %v", err)
		}
		// Always reconcile on a warm start (no opt-out): an LLM-first guarantee that a
		// restart never serves nodes for files deleted — or newly .docgraphignore'd —
		// while serve was down. A cold start ⟹ fresh DB ⟹ nothing to reconcile, so
		// `warm` is a pure no-op skip, not a behavioral knob.
		if warm {
			reconcileWorkspaceProjects(w.Projects, w.NoGitignore) // PARITY: single --path branch calls reconcileDeletedFiles directly
		}
	}
	if !warm {
		setIndexing(true)
	}
	go doSync()
	var paths []string
	for _, proj := range w.Projects {
		paths = append(paths, proj.Path)
	}
	go watchWorkspaceProjects(w, paths, maxWatches)
	return w
}

// watchWorkspaceProjects runs the fsnotify loop for all workspace project paths.
func watchWorkspaceProjects(w *workspace.Workspace, paths []string, maxWatches int) {
	if err := watcher.WatchWithLimit(paths, maxWatches, func(projectPath string, files []string) {
		for _, proj := range w.Projects {
			if proj.Path == projectPath {
				fmt.Fprintf(os.Stderr, "[watcher] re-indexing %s\n", proj.Name)
				pruneDeletedFiles(proj.Path, proj.Store, files)
				workspace.ReindexProject(proj)
				// An edited .docgraphignore/.gitignore changes which files are in
				// scope; prune any now-excluded files via the ignore-aware reconcile.
				if containsIgnoreRuleFile(files) {
					if m, mErr := scanner.NewIgnoreMatcher(proj.Path, scanner.ScanOptions{NoGitignore: w.NoGitignore}); mErr == nil {
						reconcileDeletedFiles(proj.Path, proj.Store, m)
					}
				}
				break
			}
		}
	}); err != nil {
		log.Printf("[serve] watcher: %v", err)
	}
}

// serveSinglePath wires the --path (single-project) arm: opens the store,
// registers single-project tools on srv, starts background indexing and the watcher.
// Returns a Closer that must be deferred by the caller (after stdio.Listen returns).
func serveSinglePath(srv *mcp.MCPServer, path string, maxWatches int, regOpts tools.RegisterOpts) io.Closer {
	if path == "" {
		path = "."
	}
	absRoot, err := filepath.Abs(path)
	if err != nil {
		log.Fatal(err)
	}
	warm := dbExists(path)
	st := openStore(path)
	setIndexing := tools.RegisterWithOpts(srv, st, absRoot, regOpts)
	doSync := func() {
		defer setIndexing(false)
		// force=false: incremental — the per-file stale-row deletes are
		// load-bearing here (the DB is not freshly wiped). Only `index --force`
		// (removeIndexDB) skips them.
		if err := indexStore(absRoot, st, false); err != nil {
			fmt.Fprintf(os.Stderr, "[sync] %v\n", err)
		}
		// Always reconcile on a warm start (no opt-out — see the --workspace branch note).
		if warm {
			m, _ := scanner.NewIgnoreMatcher(absRoot, scanner.ScanOptions{NoGitignore: noGitignore})
			reconcileDeletedFiles(absRoot, st, m) // PARITY: keep in sync with the --workspace branch
		}
	}
	if !warm {
		setIndexing(true)
	}
	go doSync()
	go watchSinglePath(st, absRoot, maxWatches)
	return st
}

// watchSinglePath runs the fsnotify loop for a single project path.
func watchSinglePath(st *store.Store, absRoot string, maxWatches int) {
	err := watcher.WatchWithLimit([]string{absRoot}, maxWatches, func(projectPath string, files []string) {
		fmt.Fprintf(os.Stderr, "[watcher] re-indexing %s\n", projectPath)
		pruneDeletedFiles(projectPath, st, files)
		// force=false: incremental re-index of changed files; deletes stay.
		if err := indexStore(projectPath, st, false); err != nil {
			fmt.Fprintf(os.Stderr, "[watcher] re-index %s: %v\n", projectPath, err)
		}
		// An edited .docgraphignore/.gitignore changes which files are in scope;
		// prune any now-excluded files via the ignore-aware reconcile.
		if containsIgnoreRuleFile(files) {
			if m, mErr := scanner.NewIgnoreMatcher(projectPath, scanner.ScanOptions{NoGitignore: noGitignore}); mErr == nil {
				reconcileDeletedFiles(projectPath, st, m)
			}
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[watcher] %v\n", err)
	}
}
