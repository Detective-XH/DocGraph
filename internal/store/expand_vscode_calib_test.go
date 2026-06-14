package store

import (
	"fmt"
	"os"
	"testing"
)

// TestExpandQueryTermsVscodeCalibration runs the Fix A differential calibration
// against a real vscode corpus DB. Gated by DG_VSCODE_CALIB=1 and DG_VSCODE_DB=<path>.
// NOT for CI — run locally before opening the PR, then confirm pass/fail only
// (no paths) in the shipped archive.
//
//	DG_VSCODE_CALIB=1 DG_VSCODE_DB=/path/to/vscode/.docgraph/docgraph.db \
//	  go test -run TestExpandQueryTermsVscodeCalibration -v ./internal/store/
func TestExpandQueryTermsVscodeCalibration(t *testing.T) {
	if os.Getenv("DG_VSCODE_CALIB") == "" {
		t.Skip("set DG_VSCODE_CALIB=1 and DG_VSCODE_DB=<path> to run vscode calibration")
	}
	dbPath := os.Getenv("DG_VSCODE_DB")
	if dbPath == "" {
		t.Fatal("DG_VSCODE_DB must be set to the vscode .docgraph/docgraph.db path")
	}

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Representative terms from actual vscode heading content
	terms := []string{
		"debug", "extension", "workspace", "settings",
		"keybinding", "configuration", "terminal", "editor",
	}

	allPassed := true
	for _, term := range terms {
		req := searchRequest{Terms: []string{term}}
		likeExpanded := st.Searcher.expandQueryTermsLike(req)
		ftsExpanded := st.Searcher.expandQueryTerms(req)

		ftsSet := make(map[string]bool)
		for _, tok := range ftsExpanded {
			ftsSet[tok] = true
		}

		var missing []string
		for _, want := range likeExpanded {
			if !ftsSet[want] {
				missing = append(missing, want)
			}
		}

		if len(missing) > 0 {
			t.Errorf("term=%q: FTS missing LIKE terms: %v (LIKE=%d FTS=%d)",
				term, missing, len(likeExpanded), len(ftsExpanded))
			allPassed = false
		} else {
			extra := extraFTSTerms(ftsExpanded, likeExpanded)
			t.Logf("PASS term=%q: LIKE=%d FTS=%d extra=%v",
				term, len(likeExpanded), len(ftsExpanded), extra)
		}
	}
	if allPassed {
		fmt.Println("VSCODE CALIBRATION: PASS — FTS ⊇ LIKE for all terms")
	}
}

func extraFTSTerms(fts, like []string) []string {
	likeSet := make(map[string]bool)
	for _, t := range like {
		likeSet[t] = true
	}
	var extra []string
	for _, t := range fts {
		if !likeSet[t] {
			extra = append(extra, t)
		}
	}
	return extra
}
