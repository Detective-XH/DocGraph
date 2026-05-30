package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// mockHistoryReader returns a canned FileHistory, proving handleHistory's render
// path (historyText) is unit-testable with no real *store.Store / git / SQLite.
type mockHistoryReader struct {
	hist  *store.FileHistory
	err   error
	calls int
}

func (m *mockHistoryReader) GetFileHistory(string) (*store.FileHistory, error) {
	m.calls++
	return m.hist, m.err
}

var _ HistoryReader = (*mockHistoryReader)(nil)

func TestHistoryText(t *testing.T) {
	t.Run("renders full history with plurals", func(t *testing.T) {
		m := &mockHistoryReader{hist: &store.FileHistory{
			CommitCount: 3, AuthorCount: 2,
			LastAuthor: "Ada", LastSubject: "tidy up",
			FirstCommitAt: 1700000000, LastCommitAt: 1710000000,
		}}
		out, err := historyText(m, "Guide", "docs/guide.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.calls != 1 {
			t.Fatalf("expected exactly 1 GetFileHistory call, got %d", m.calls)
		}
		for _, want := range []string{
			"## History: Guide", "**Path:** docs/guide.md",
			"amended **3 times** by **2 authors**",
			"**Last author:** Ada", "**Last commit:** tidy up",
			"**First changed:** 2023-11-14", "**Last changed:** 2024-03-09",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q\n--- output ---\n%s", want, out)
			}
		}
	})

	t.Run("singular for one commit / one author", func(t *testing.T) {
		m := &mockHistoryReader{hist: &store.FileHistory{CommitCount: 1, AuthorCount: 1}}
		out, _ := historyText(m, "X", "x.md")
		if !strings.Contains(out, "amended **1 time** by **1 author**") {
			t.Fatalf("expected singular wording, got:\n%s", out)
		}
	})

	t.Run("nil history -> no-git message", func(t *testing.T) {
		out, err := historyText(&mockHistoryReader{hist: nil}, "X", "x.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "No git history found") {
			t.Fatalf("expected no-history message, got:\n%s", out)
		}
	})

	t.Run("zero commits -> no-git message", func(t *testing.T) {
		out, _ := historyText(&mockHistoryReader{hist: &store.FileHistory{CommitCount: 0}}, "X", "x.md")
		if !strings.Contains(out, "No git history found") {
			t.Fatalf("expected no-history message for zero commits, got:\n%s", out)
		}
	})

	t.Run("store error propagates", func(t *testing.T) {
		if _, err := historyText(&mockHistoryReader{err: errors.New("boom")}, "X", "x.md"); err == nil {
			t.Fatal("expected error to propagate")
		}
	})
}
