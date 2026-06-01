package store

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func requireExpandBench(b *testing.B) {
	b.Helper()
	if os.Getenv("DG_QUERY_BENCH") == "" {
		b.Skip("set DG_QUERY_BENCH=1 to run query-serving benchmarks")
	}
}

// seedExpandCorpus seeds nDocs synthetic documents with heading/definition/tag
// nodes. A controlled fraction embed the bench query tokens in their Name fields
// so expandQueryTerms exercises the full row-scan + addTerm path, not the
// zero-match fast exit (which the tools-level bench corpus fails to exercise).
func seedExpandCorpus(tb testing.TB, st *Store, nDocs int) int {
	tb.Helper()
	rng := rand.New(rand.NewSource(42))
	vocab := []string{
		"service", "cluster", "node", "pod", "container", "registry", "image",
		"network", "ingress", "egress", "policy", "config", "secret", "namespace",
		"resource", "limit", "scale", "replica", "rollout", "canary",
		"pipeline", "build", "artifact", "release", "stage", "environment",
		"monitor", "metric", "alert", "dashboard", "trace", "latency",
		"storage", "backup", "restore", "snapshot", "retention", "archive",
		"auth", "token", "session", "credential", "rotation", "audit",
		"gateway", "proxy", "loadbalancer", "upstream", "downstream", "retry",
	}
	capitalize := func(s string) string {
		if s == "" {
			return s
		}
		return string(s[0]-32) + s[1:]
	}
	word := func() string { return vocab[rng.Intn(len(vocab))] }
	nowUnix := time.Now().Unix()
	var nodes []Node

	for i := 0; i < nDocs; i++ {
		path := fmt.Sprintf("docs/doc%04d.md", i)
		nodes = append(nodes, Node{
			ID: path, Kind: "document",
			Name: fmt.Sprintf("Doc %04d", i), QualifiedName: path, FilePath: path,
			StartLine: 1, EndLine: 200, UpdatedAt: nowUnix,
		})
		nHeadings := 8 + rng.Intn(3)
		for h := 0; h < nHeadings; h++ {
			var hName string
			// every 5th doc, first heading gets the bench query tokens in its name
			if i%5 == 0 && h == 0 {
				hName = "Kubernetes Deployment Overview"
			} else {
				hName = capitalize(word()) + " " + capitalize(word())
			}
			nodes = append(nodes, Node{
				ID: fmt.Sprintf("%s#h%d", path, h), Kind: "heading",
				Name: hName, QualifiedName: fmt.Sprintf("%s#h%d", path, h),
				FilePath: path, StartLine: 10 + h*20, EndLine: 29 + h*20,
				Level: 1 + h%3, UpdatedAt: nowUnix,
			})
		}
		defName := word() + "-" + word()
		if i%7 == 0 {
			defName = "api-deployment"
		}
		nodes = append(nodes, Node{
			ID: fmt.Sprintf("%s#def0", path), Kind: "definition",
			Name: defName, QualifiedName: fmt.Sprintf("%s#def0", path),
			FilePath: path, StartLine: 5, EndLine: 6, UpdatedAt: nowUnix,
		})
		tagName := word()
		if i%11 == 0 {
			tagName = "kubernetes-infra"
		}
		tagID := "tag:" + tagName
		nodes = append(nodes, Node{
			ID: tagID, Kind: "tag", Name: tagName, QualifiedName: tagID,
			FilePath: "", StartLine: 0, EndLine: 0, UpdatedAt: nowUnix,
		})
	}
	if err := st.InsertNodes(nodes); err != nil {
		tb.Fatalf("seedExpandCorpus InsertNodes: %v", err)
	}
	return len(nodes)
}

// tempStoreB opens a temp-dir SQLite store for benchmarks.
// tempStore in store_test.go takes *testing.T; this variant accepts *testing.B.
func tempStoreB(b *testing.B) *Store {
	b.Helper()
	st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	return st
}

// BenchmarkExpandQueryTerms measures the isolated cost of a single
// expandQueryTerms call against a correctly-seeded ~33k-node corpus.
//
//	DG_QUERY_BENCH=1 go test -run=^$ -bench=BenchmarkExpandQueryTerms \
//	    -benchtime=5s -count=3 -cpuprofile=/tmp/eq.prof ./internal/store/
//	go tool pprof -focus=expandQueryTerms -top -cum /tmp/eq.prof
func BenchmarkExpandQueryTerms(b *testing.B) {
	requireExpandBench(b)
	nDocs := 3000
	if v := os.Getenv("DG_EXPAND_BENCH_DOCS"); v != "" {
		var n int
		if cnt, _ := fmt.Sscanf(v, "%d", &n); cnt == 1 && n > 0 {
			nDocs = n
		}
	}
	st := tempStoreB(b)
	nNodes := seedExpandCorpus(b, st, nDocs)
	b.Logf("expand bench corpus: %d docs, %d nodes", nDocs, nNodes)

	req := searchRequest{Terms: []string{"kubernetes", "deployment"}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = st.expandQueryTerms(req)
	}
}
