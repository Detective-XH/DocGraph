package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/install"
)

func cmdInstall(args []string) {
	fset := flag.NewFlagSet("install", flag.ExitOnError)
	clients := fset.String("clients", "auto", "Install MCP config for clients: auto, all, or comma-separated client names")
	workspaceMode := fset.Bool("workspace", false, "Configure clients to use serve --workspace")
	scope := fset.String("scope", "", "Installation scope for Claude Code: 'user' registers globally via claude mcp add")
	updateSkills := fset.Bool("update-skills", false, "Re-install bundled skills, overwriting existing files")
	dryRun := fset.Bool("dry-run", false, "Print planned changes without writing files")
	interactive := fset.Bool("interactive", false, "Review planned changes and ask before writing")
	fset.Parse(args)
	dir := "."
	if fset.NArg() > 0 {
		dir = fset.Arg(0)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	opts := install.Options{Clients: *clients, Workspace: *workspaceMode, Scope: *scope, DryRun: *dryRun}
	if *dryRun || *interactive {
		planned, err := install.Plan(root, opts)
		if err != nil {
			log.Fatal(err)
		}
		printInstallPlan(planned)
		if *dryRun {
			return
		}
		if !confirm(os.Stdin, os.Stderr, "Apply installer changes?") {
			fmt.Fprintln(os.Stderr, "Install cancelled")
			return
		}
	}
	results, err := install.Apply(root, opts)
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

func printInstallPlan(results []install.Result) {
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No MCP client configs selected")
		return
	}
	fmt.Fprintln(os.Stderr, "Installer review:")
	for _, result := range results {
		if result.Detail == "" {
			fmt.Fprintf(os.Stderr, "  %s %-9s %s\n", result.Client, result.Action, result.Path)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %s %-9s %s — %s\n", result.Client, result.Action, result.Path, result.Detail)
	}
}

func confirm(r io.Reader, w io.Writer, prompt string) bool {
	fmt.Fprintf(w, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes"
}
