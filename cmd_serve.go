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

func cmdServe(args []string) {
	fset := flag.NewFlagSet("serve", flag.ExitOnError)
	p := fset.String("path", "", "Project directory to index and serve")
	ws := fset.String("workspace", "", "Workspace root (index all child dirs as projects)")
	fset.BoolVar(&noGitignore, "no-gitignore", false, "Ignore .gitignore rules, index all .md files")
	fset.Float64Var(&similarityThreshold, "threshold", 0, "Similarity threshold for similar_to edges (default 0.25)")
	fset.Parse(args)

	srv := mcp.NewMCPServer("docgraph", "0.1.0", mcp.WithInstructions(serverInstructions))

	if *ws != "" {
		w, err := workspace.Open(*ws)
		if err != nil {
			log.Fatal(err)
		}
		defer w.Close()
		w.NoGitignore = noGitignore
		w.SimilarityThreshold = similarityThreshold
		w.IndexAll()
		tools.RegisterWorkspace(srv, w)
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
		st := indexPath(path)
		defer st.Close()
		tools.Register(srv, st, absRoot)
		go func() {
			err := watcher.Watch([]string{absRoot}, func(projectPath string, _ []string) {
				fmt.Fprintf(os.Stderr, "[watcher] re-indexing %s\n", projectPath)
				if err := indexStore(projectPath, st); err != nil {
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
