package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	fset.Float64Var(&similarityThreshold, "threshold", 0, "Similarity threshold for similar_to edges (default 0.25)")
	enableEmbeddings := fset.Bool("enable-embeddings", false, "Register docgraph_embeddings (sends content to external LLM provider)")
	enableEnrichment := fset.Bool("enable-enrichment", false, "Register docgraph_enrichment (sends content to external LLM provider)")
	fset.Parse(args)

	regOpts := tools.RegisterOpts{
		EnableEmbeddings: *enableEmbeddings,
		EnableEnrichment: *enableEnrichment,
	}

	srv := mcp.NewMCPServer("docgraph", "0.1.0", mcp.WithInstructions(serverInstructions))

	if *ws != "" {
		warm := anyProjectDBExists(*ws)
		w, err := workspace.Open(*ws)
		if err != nil {
			log.Fatal(err)
		}
		defer w.Close()
		w.NoGitignore = noGitignore
		w.SimilarityThreshold = similarityThreshold
		setIndexing := tools.RegisterWorkspaceWithOpts(srv, w, regOpts)
		doSync := func() {
			defer setIndexing(false)
			w.IndexAll()
		}
		if !warm {
			setIndexing(true)
		}
		go doSync()
		var paths []string
		for _, proj := range w.Projects {
			paths = append(paths, proj.Path)
		}
		go watcher.Watch(paths, func(projectPath string, _ []string) {
			for _, proj := range w.Projects {
				if proj.Path == projectPath {
					fmt.Fprintf(os.Stderr, "[watcher] re-indexing %s\n", proj.Name)
					workspace.ReindexProject(proj)
					break
				}
			}
		})
	} else {
		path := *p
		if path == "" {
			path = "."
		}
		absRoot, err := filepath.Abs(path)
		if err != nil {
			log.Fatal(err)
		}
		warm := dbExists(path)
		st := openStore(path)
		defer st.Close()
		setIndexing := tools.RegisterWithOpts(srv, st, absRoot, regOpts)
		doSync := func() {
			defer setIndexing(false)
			// force=false: incremental — the per-file stale-row deletes are
			// load-bearing here (the DB is not freshly wiped). Only `index --force`
			// (removeIndexDB) skips them.
			if err := indexStore(absRoot, st, false); err != nil {
				fmt.Fprintf(os.Stderr, "[sync] %v\n", err)
			}
		}
		if !warm {
			setIndexing(true)
		}
		go doSync()
		go func() {
			err := watcher.Watch([]string{absRoot}, func(projectPath string, _ []string) {
				fmt.Fprintf(os.Stderr, "[watcher] re-indexing %s\n", projectPath)
				// force=false: incremental re-index of changed files; deletes stay.
				if err := indexStore(projectPath, st, false); err != nil {
					fmt.Fprintf(os.Stderr, "[watcher] re-index %s: %v\n", projectPath, err)
				}
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "[watcher] %v\n", err)
			}
		}()
	}

	stdio := mcp.NewStdioServer(srv)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
