package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Detective-XH/docgraph/internal/install"
)

func cmdInstall(args []string) {
	fset := flag.NewFlagSet("install", flag.ExitOnError)
	clients := fset.String("clients", "auto", "Install MCP config for clients: auto, all, or comma-separated client names")
	workspaceMode := fset.Bool("workspace", false, "Configure clients to use serve --workspace")
	scope := fset.String("scope", "", "Installation scope for Claude Code: 'user' registers globally via claude mcp add")
	updateSkills := fset.Bool("update-skills", false, "Re-install bundled skills, overwriting existing files")
	fset.Parse(args)
	dir := "."
	if fset.NArg() > 0 {
		dir = fset.Arg(0)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	results, err := install.Apply(root, install.Options{Clients: *clients, Workspace: *workspaceMode, Scope: *scope})
	if err != nil {
		log.Fatal(err)
	}
	printInstallResults(results)
	if claudeInstalled(results) {
		if err := installSkills(root, false); err != nil {
			log.Printf("warning: skills install: %v", err)
		}
	}
	if *updateSkills {
		if err := installSkills(root, true); err != nil {
			log.Fatal(err)
		}
	}
}

func claudeInstalled(results []install.Result) bool {
	for _, r := range results {
		if install.IsClaudeResult(r) {
			_, err := exec.LookPath("claude")
			return err == nil
		}
	}
	return false
}

func printInstallResults(results []install.Result) {
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No MCP client configs were updated")
		return
	}
	for _, result := range results {
		fmt.Fprintf(os.Stderr, "Configured %s: %s\n", result.Client, result.Path)
	}
}
