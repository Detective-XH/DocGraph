package main

import (
	"fmt"
	"os"
)

func main() {
	defer startProfiling()()
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "pack":
		cmdPack(os.Args[2:])
	case "index":
		cmdIndex(os.Args[2:])
	case "sync":
		cmdSync(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		os.Exit(0)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: docgraph <command>\n\nCommands:\n  init [--dry-run] [--interactive] [--install-clients auto|all|claude,codex,hermes,opencode] [--workspace] [--scope user] [--with-skills] [--update-skills] [path]\n  install [--dry-run] [--interactive] [--clients auto|all|claude,codex,hermes,opencode] [--workspace] [--scope user] [--update-skills] [path]\n  pack list [--workspace] <path>\n  pack enable [--workspace] [--no-sync] <pack-id> <path>\n  pack disable [--workspace] <pack-id> <path>\n  index [--force] [--threshold N] [--no-gitignore] [--no-history] <path>\n  sync [--threshold N] [--no-gitignore] [--no-history] <path>\n  status <path>\n  serve [--threshold N] [--no-gitignore] [--no-history] [--enable-embeddings] [--enable-enrichment] [--no-reconcile-on-start] --path <path>\n  serve [--threshold N] [--no-gitignore] [--no-history] [--enable-embeddings] [--enable-enrichment] [--no-reconcile-on-start] --workspace <dir>\n  version\n")
	os.Exit(1)
}

var version = "dev"

var noGitignore bool
var noHistory bool
var similarityThreshold float64
