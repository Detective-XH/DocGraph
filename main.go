package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
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
	fmt.Fprintf(os.Stderr, "Usage: docgraph <command>\n\nCommands:\n  init [--install-clients auto|all|claude,codex,hermes,opencode] [--workspace] [--scope user] [--with-skills] [--update-skills] [path]\n  install [--clients auto|all|claude,codex,hermes,opencode] [--workspace] [--scope user] [--update-skills] [path]\n  index [--force] [--threshold N] <path>\n  sync [--threshold N] <path>\n  status <path>\n  serve [--threshold N] --path <path>\n  serve [--threshold N] --workspace <dir>\n  version\n")
	os.Exit(1)
}

var version = "dev"

var noGitignore bool
var similarityThreshold float64
