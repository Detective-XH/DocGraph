package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/install"
)

func cmdInit(args []string) {
	fset := flag.NewFlagSet("init", flag.ExitOnError)
	installClients := fset.String("install-clients", "", "Install MCP config for clients: auto, all, or comma-separated client names")
	workspaceMode := fset.Bool("workspace", false, "Configure installed clients to use serve --workspace")
	scope := fset.String("scope", "", "Installation scope for Claude Code: 'user' registers globally via claude mcp add")
	withSkills := fset.Bool("with-skills", false, "Copy bundled skills to .claude/skills/ (skips existing directories)")
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
	if err := initProject(root); err != nil {
		log.Fatal(err)
	}
	if *withSkills {
		if err := installSkills(root, false); err != nil {
			log.Fatal(err)
		}
	}
	if *installClients != "" {
		results, err := install.Apply(root, install.Options{Clients: *installClients, Workspace: *workspaceMode, Scope: *scope})
		if err != nil {
			log.Fatal(err)
		}
		printInstallResults(results)
		if claudeInstalled(results) {
			if err := installSkills(root, false); err != nil {
				log.Printf("warning: skills install: %v", err)
			}
		}
	}
	if *updateSkills {
		if err := installSkills(root, true); err != nil {
			log.Fatal(err)
		}
	}
}

func initProject(root string) error {
	if err := os.MkdirAll(filepath.Join(root, ".docgraph"), 0o755); err != nil {
		return err
	}

	docgraphIgnore := filepath.Join(root, ".docgraphignore")
	if _, err := os.Stat(docgraphIgnore); errors.Is(err, os.ErrNotExist) {
		content := "# DocGraph ignore patterns\n# Uses .gitignore syntax.\n\n"
		if err := os.WriteFile(docgraphIgnore, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Created %s\n", docgraphIgnore)
	}

	gitignore := filepath.Join(root, ".gitignore")
	if err := ensureGitignoreLine(gitignore, ".docgraph/"); err != nil {
		return err
	}

	mcpConfig := filepath.Join(root, ".mcp.json")
	if _, err := os.Stat(mcpConfig); errors.Is(err, os.ErrNotExist) {
		content := `{
  "mcpServers": {
    "docgraph": {
      "command": "docgraph",
      "args": ["serve", "--path", "."]
    }
  }
}
`
		if err := os.WriteFile(mcpConfig, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Created %s\n", mcpConfig)
	}

	fmt.Fprintf(os.Stderr, "Initialized DocGraph in %s\n", root)
	return nil
}

func ensureGitignoreLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for _, existing := range lines {
		if strings.TrimSpace(existing) == line {
			return nil
		}
	}
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if err := os.WriteFile(path, append(data, []byte(prefix+line+"\n")...), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Updated %s\n", path)
	return nil
}
