package main

import (
	"flag"
	"log"
)

func cmdIndex(args []string) {
	fset := flag.NewFlagSet("index", flag.ExitOnError)
	force := fset.Bool("force", false, "Delete the existing .docgraph database before indexing")
	fset.BoolVar(&noGitignore, "no-gitignore", false, "Ignore .gitignore rules, index all .md files")
	fset.BoolVar(&noHistory, "no-history", false, "Skip git commit-history collection (file_history; on by default)")
	fset.Float64Var(&similarityThreshold, "threshold", 0, "Similarity threshold for similar_to edges (default 0.25)")
	fset.Parse(args)
	if fset.NArg() < 1 {
		log.Fatal("usage: docgraph index [--force] [--threshold N] <path>")
	}
	indexPathOpts(fset.Arg(0), *force).Close()
}

func cmdSync(args []string) {
	fset := flag.NewFlagSet("sync", flag.ExitOnError)
	fset.BoolVar(&noGitignore, "no-gitignore", false, "Ignore .gitignore rules, index all .md files")
	fset.BoolVar(&noHistory, "no-history", false, "Skip git commit-history collection (file_history; on by default)")
	fset.Float64Var(&similarityThreshold, "threshold", 0, "Similarity threshold for similar_to edges (default 0.25)")
	fset.Parse(args)
	if fset.NArg() < 1 {
		log.Fatal("usage: docgraph sync [--threshold N] <path>")
	}
	indexPath(fset.Arg(0)).Close()
}
