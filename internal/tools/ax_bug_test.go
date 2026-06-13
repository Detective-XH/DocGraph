package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/workspace"
)

// TestStatusReportsIgnoreConfig verifies docgraph_status surfaces the ignore
// configuration, so an agent can discover .docgraphignore (and how to exclude
// files) from the MCP surface alone, without reading the README.
func TestStatusReportsIgnoreConfig(t *testing.T) {
	st := seedStatusStore(t, "a.md", 0)
	h := &handler{store: st, projectRoot: t.TempDir(), noGitignore: true}

	res, err := callTool(h, h.handleStatus, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)
	for _, want := range []string{"### Index Configuration", ".docgraphignore", "--no-gitignore"} {
		if !strings.Contains(text, want) {
			t.Errorf("status output missing %q\n--- output ---\n%s", want, text)
		}
	}
}

// TestDriftAuditHonorsProjectFilter verifies format=drift_audit scopes to the named
// project instead of fanning out across the whole workspace (the silent no-op the
// AX report opened with).
func TestDriftAuditHonorsProjectFilter(t *testing.T) {
	h := &handler{workspace: &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "project-a", Path: t.TempDir(), Store: seedStatusStore(t, "a.md", 0)},
		{Name: "project-b", Path: t.TempDir(), Store: seedStatusStore(t, "b.md", 0)},
	}}}

	scoped := h.renderDriftAudit("", "project-a")
	if !strings.Contains(scoped, "## project-a") {
		t.Errorf("scoped report should include project-a:\n%s", scoped)
	}
	if strings.Contains(scoped, "## project-b") {
		t.Errorf("scoped report must NOT include project-b:\n%s", scoped)
	}

	all := h.renderDriftAudit("", "")
	if !strings.Contains(all, "## project-a") || !strings.Contains(all, "## project-b") {
		t.Errorf("unscoped report should include both projects:\n%s", all)
	}

	none := h.renderDriftAudit("", "nonexistent")
	if !strings.Contains(none, "No project named") {
		t.Errorf("unknown project should report no match:\n%s", none)
	}
}
