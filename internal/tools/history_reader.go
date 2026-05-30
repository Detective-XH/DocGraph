package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
)

// HistoryReader is the git-history surface handleHistory consumes from a store.
// GetFileHistory is the only store call on the render path (node resolution
// stays in the handler), so the rendering is unit-testable against a mock.
// *store.Store satisfies it.
type HistoryReader interface {
	GetFileHistory(path string) (*store.FileHistory, error)
}

var _ HistoryReader = (*store.Store)(nil)

// historyText renders the docgraph_history report for an already-resolved node.
// It is pure apart from the single GetFileHistory call through r, which is what
// makes it mockable.
func historyText(r HistoryReader, name, filePath string) (string, error) {
	hist, err := r.GetFileHistory(filePath)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## History: %s\n\n", name)
	fmt.Fprintf(&sb, "**Path:** %s\n", filePath)

	if hist == nil || hist.CommitCount == 0 {
		sb.WriteString("\nNo git history found. The file may be untracked or outside a git repository.\n")
		return sb.String(), nil
	}

	amendWord := "time"
	if hist.CommitCount != 1 {
		amendWord = "times"
	}
	authorWord := "author"
	if hist.AuthorCount != 1 {
		authorWord = "authors"
	}

	fmt.Fprintf(&sb, "**Commits:** %d — amended **%d %s** by **%d %s**\n",
		hist.CommitCount, hist.CommitCount, amendWord, hist.AuthorCount, authorWord)
	if hist.LastAuthor != "" {
		fmt.Fprintf(&sb, "**Last author:** %s\n", hist.LastAuthor)
	}
	if hist.LastSubject != "" {
		fmt.Fprintf(&sb, "**Last commit:** %s\n", hist.LastSubject)
	}
	if hist.FirstCommitAt > 0 {
		fmt.Fprintf(&sb, "**First changed:** %s\n", time.Unix(hist.FirstCommitAt, 0).UTC().Format("2006-01-02"))
	}
	if hist.LastCommitAt > 0 {
		fmt.Fprintf(&sb, "**Last changed:** %s\n", time.Unix(hist.LastCommitAt, 0).UTC().Format("2006-01-02"))
	}

	return sb.String(), nil
}
