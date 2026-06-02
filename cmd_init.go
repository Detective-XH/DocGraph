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
	if *dryRun || *interactive {
		printInitPlan(root, *installClients, install.Options{
			Clients:   *installClients,
			Workspace: *workspaceMode,
			Scope:     *scope,
		}, *withSkills, *updateSkills)
		if *dryRun {
			return
		}
		if !confirm(os.Stdin, os.Stderr, "Apply init changes?") {
			fmt.Fprintln(os.Stderr, "Init cancelled")
			return
		}
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

func printInitPlan(root, installClients string, opts install.Options, withSkills, updateSkills bool) {
	fmt.Fprintln(os.Stderr, "Init review:")
	for _, item := range planInitProject(root) {
		fmt.Fprintf(os.Stderr, "  %-9s %s — %s\n", item.action, item.path, item.detail)
	}
	if installClients != "" {
		planned, err := install.Plan(root, opts)
		if err != nil {
			log.Fatal(err)
		}
		printInstallPlan(planned)
	}
	if withSkills {
		fmt.Fprintf(os.Stderr, "  skills    %s — install bundled skills, skip existing\n", filepath.Join(root, ".claude", "skills"))
	}
	if updateSkills {
		fmt.Fprintf(os.Stderr, "  skills    %s — overwrite bundled skills\n", filepath.Join(root, ".claude", "skills"))
	}
}

type initPlanItem struct {
	action string
	path   string
	detail string
}

func planInitProject(root string) []initPlanItem {
	return []initPlanItem{
		planDir(filepath.Join(root, ".docgraph")),
		planFile(filepath.Join(root, ".docgraphignore"), "create DocGraph ignore template"),
		planGitignore(filepath.Join(root, ".gitignore"), ".docgraph/"),
		planFile(filepath.Join(root, ".mcp.json"), "create project-local MCP config when missing"),
	}
}

func planDir(path string) initPlanItem {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return initPlanItem{action: "unchanged", path: path, detail: "directory exists"}
	}
	return initPlanItem{action: "create", path: path, detail: "create directory"}
}

func planFile(path, createDetail string) initPlanItem {
	if _, err := os.Stat(path); err == nil {
		return initPlanItem{action: "unchanged", path: path, detail: "file exists"}
	}
	return initPlanItem{action: "create", path: path, detail: createDetail}
}

func planGitignore(path, line string) initPlanItem {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return initPlanItem{action: "create", path: path, detail: "create .gitignore with " + line}
	}
	if err != nil {
		return initPlanItem{action: "inspect", path: path, detail: err.Error()}
	}
	for existing := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(existing) == line {
			return initPlanItem{action: "unchanged", path: path, detail: line + " already present"}
		}
	}
	return initPlanItem{action: "update", path: path, detail: "append " + line}
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
		args := `["serve", "--path", "."]`
		content := fmt.Sprintf(`{
  "mcpServers": {
    "docgraph": {
      "command": "docgraph",
      "args": %s
    }
  }
}
`, args)
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
	for existing := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(existing) == line {
			return nil
		}
	}
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if err := os.WriteFile(path, append(data, []byte(prefix+line+"\n")...), 0o644); err != nil { // #nosec G703 -- path is an operator-supplied gitignore file path; ensureGitignoreLine is only called from the CLI init command
		return err
	}
	fmt.Fprintf(os.Stderr, "Updated %s\n", path)
	return nil
}
