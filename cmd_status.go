package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	if schemaVer, schemaName, err := st.SchemaVersion(); err == nil && schemaVer > 0 {
		fmt.Printf("  Schema: v%d (%s)\n", schemaVer, schemaName)
	}
	if packStats, err := st.GetDomainPackStats(); err == nil && packStats.TotalPacks > 0 {
		fmt.Printf("  Domain Packs: %d loaded (%d enabled, %d fields)\n",
			packStats.TotalPacks, packStats.EnabledPacks, packStats.TotalFields)
	}
	if qualityStats, err := st.GetMetadataQualityStats(time.Time{}); err == nil && qualityStats.TotalDocs > 0 {
		fmt.Printf("  Metadata Quality: %.1f/100 avg (good: %d, warning: %d, poor: %d)\n",
			qualityStats.AverageScore, qualityStats.GoodDocs, qualityStats.WarningDocs, qualityStats.PoorDocs)
		if top := topQualityIssues(qualityStats.IssueCounts, 3); top != "" {
			fmt.Printf("  Metadata Quality Issues: %s\n", top)
		}
	}
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

func topQualityIssues(counts map[string]int, limit int) string {
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool {
		if counts[codes[i]] == counts[codes[j]] {
			return codes[i] < codes[j]
		}
		return counts[codes[i]] > counts[codes[j]]
	})
	if limit > 0 && len(codes) > limit {
		codes = codes[:limit]
	}
	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		parts = append(parts, fmt.Sprintf("%s:%d", code, counts[code]))
	}
	return strings.Join(parts, ", ")
}
