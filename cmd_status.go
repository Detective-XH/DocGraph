package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"github.com/Detective-XH/docgraph/internal/store"
)

func cmdStatus(args []string) {
	fset := flag.NewFlagSet("status", flag.ExitOnError)
	fset.Parse(args)
	if fset.NArg() < 1 {
		log.Fatal("usage: docgraph status <path>")
	}
	root, err := filepath.Abs(fset.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, ".docgraph", "docgraph.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	s, err := st.GetStats()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("DocGraph Index Status\n  Files: %d\n  Nodes: %d (%s)\n  Edges: %d (%s)\n  Unresolved: %d\n  DB Size: %s\n",
		s.FileCount, s.NodeCount, kindStr(s.NodesByKind), s.EdgeCount, kindStr(s.EdgesByKind), s.UnresolvedCount, sizeStr(s.DBSizeBytes))
}

func sizeStr(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func kindStr(m map[string]int) string {
	s := ""
	for k, v := range m {
		if s != "" {
			s += ", "
		}
		s += fmt.Sprintf("%s: %d", k, v)
	}
	return s
}
