package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

const codeDocPackID = "code_doc"

func cmdPack(args []string) {
	if len(args) == 0 {
		log.Fatal("usage: docgraph pack <list|enable|disable> ...")
	}

	switch args[0] {
	case "list":
		cmdPackList(args[1:])
	case "enable":
		cmdPackSet(args[1:], true)
	case "disable":
		cmdPackSet(args[1:], false)
	default:
		log.Fatalf("unknown pack command %q", args[0]) // #nosec G706 -- %q quotes the value, neutralizing log/format injection
	}
}

func cmdPackList(args []string) {
	fset := flag.NewFlagSet("pack list", flag.ExitOnError)
	workspaceMode := fset.Bool("workspace", false, "List domain packs for every child project")
	fset.Parse(args)
	if fset.NArg() != 1 {
		log.Fatal("usage: docgraph pack list [--workspace] <path>")
	}

	if *workspaceMode {
		w := openWorkspaceForPack(fset.Arg(0))
		defer w.Close()
		for _, p := range w.Projects {
			fmt.Printf("[%s]\n", p.Name)
			printPackList(p.Store)
		}
		return
	}

	st := openStoreForPack(fset.Arg(0))
	defer st.Close()
	printPackList(st)
}

func cmdPackSet(args []string, enabled bool) {
	fset := flag.NewFlagSet("pack set", flag.ExitOnError)
	workspaceMode := fset.Bool("workspace", false, "Apply the pack change to every child project")
	noSync := fset.Bool("no-sync", false, "Only change pack state; do not run follow-up indexing")
	fset.Parse(args)
	if fset.NArg() != 2 {
		action := "enable"
		if !enabled {
			action = "disable"
		}
		log.Fatalf("usage: docgraph pack %s [--workspace] [--no-sync] <pack-id> <path>", action)
	}

	packID := normalizePackID(fset.Arg(0))
	path := fset.Arg(1)

	if *workspaceMode {
		w := openWorkspaceForPack(path)
		defer w.Close()
		for _, p := range w.Projects {
			applyPackState(p.Store, p.Path, packID, enabled, *noSync)
			fmt.Printf("[%s] %s %s\n", p.Name, packID, enabledWord(enabled))
		}
		return
	}

	st := openStoreForPack(path)
	applyPackState(st, path, packID, enabled, *noSync)
	st.Close()
	fmt.Printf("%s %s\n", packID, enabledWord(enabled))
}

func printPackList(st *store.Store) {
	packs, err := st.GetDomainPacks()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("PACK\tDOMAIN\tVERSION\tENABLED\tFIELDS\tDESCRIPTION")
	for _, pack := range packs {
		enabled := "no"
		if pack.Enabled {
			enabled = "yes"
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%d\t%s\n",
			pack.ID, pack.Domain, pack.Version, enabled, len(pack.Fields), pack.Description)
	}
}

func applyPackState(st *store.Store, projectPath, packID string, enabled, noSync bool) {
	if err := st.SetPackEnabled(packID, enabled); err != nil {
		log.Fatal(err)
	}

	if packID != codeDocPackID {
		return
	}

	if !enabled {
		removed, err := st.DeleteFilesByNodeKind("code_file")
		if err != nil {
			log.Fatal(err)
		}
		if removed > 0 {
			fmt.Fprintf(os.Stderr, "removed %d indexed code_doc file(s)\n", removed)
		}
		return
	}

	if noSync {
		fmt.Fprintf(os.Stderr, "code_doc enabled; run `docgraph sync %s` before expecting code_file results\n", projectPath)
		return
	}

	// Re-open through the normal indexing path. Store.Open preserves the enabled
	// flag while syncing registered pack definitions, and the existing handle can
	// remain open because SQLite WAL mode allows concurrent connections here.
	indexPath(projectPath).Close()
}

func openStoreForPack(path string) *store.Store {
	root, err := filepath.Abs(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".docgraph"), 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, ".docgraph", "docgraph.db"))
	if err != nil {
		log.Fatal(err)
	}
	return st
}

func openWorkspaceForPack(path string) *workspace.Workspace {
	w, err := workspace.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	return w
}

func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func normalizePackID(id string) string {
	return strings.ReplaceAll(strings.TrimSpace(strings.ToLower(id)), "-", "_")
}
