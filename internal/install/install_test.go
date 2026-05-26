package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanJSONMCPDetectsCreateUpdateUnchanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	server := localServer(root, false)

	action, detail, err := planJSONMCP(path, server)
	if err != nil {
		t.Fatal(err)
	}
	if action != "create" || detail == "" {
		t.Fatalf("missing file plan = %q %q, want create with detail", action, detail)
	}

	if err := os.WriteFile(path, []byte(`{"mcpServers":{"docgraph":{"command":"old","args":[]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	action, detail, err = planJSONMCP(path, server)
	if err != nil {
		t.Fatal(err)
	}
	if action != "update" || detail == "" {
		t.Fatalf("conflicting file plan = %q %q, want update with detail", action, detail)
	}

	if err := writeJSONMCP(path, server); err != nil {
		t.Fatal(err)
	}
	action, detail, err = planJSONMCP(path, server)
	if err != nil {
		t.Fatal(err)
	}
	if action != "unchanged" || detail == "" {
		t.Fatalf("matching file plan = %q %q, want unchanged with detail", action, detail)
	}
}

func TestPlanCodexManagedBlockDetectsUpdate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	path := filepath.Join(root, "config.toml")
	server := globalServer(root, false)

	if err := os.WriteFile(path, []byte(`# BEGIN DocGraph MCP
[mcp_servers.docgraph]
command = "old"
args = []
# END DocGraph MCP
`), 0o644); err != nil {
		t.Fatal(err)
	}

	action, detail, err := planCodexTOML(path, server)
	if err != nil {
		t.Fatal(err)
	}
	if action != "update" || detail == "" {
		t.Fatalf("managed block plan = %q %q, want update with detail", action, detail)
	}

	if err := writeCodexTOML(path, server); err != nil {
		t.Fatal(err)
	}
	action, detail, err = planCodexTOML(path, server)
	if err != nil {
		t.Fatal(err)
	}
	if action != "unchanged" || detail == "" {
		t.Fatalf("matching managed block plan = %q %q, want unchanged", action, detail)
	}
}

func TestApplyDryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)

	results, err := Apply(root, Options{Clients: "claude,codex", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("dry-run results = %d, want 2", len(results))
	}
	for _, path := range []string{
		filepath.Join(root, ".mcp.json"),
		filepath.Join(root, "config.toml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote %s", path)
		}
	}
}
