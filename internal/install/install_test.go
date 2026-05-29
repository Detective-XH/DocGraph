package install

import (
	"os"
	"path/filepath"
	"strings"
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

func TestInstallServerArgsHaveNoToolProfile(t *testing.T) {
	root := t.TempDir()

	server := localServer(root, false)
	want := []string{"serve", "--path", "."}
	if !equalStrings(server.Args, want) {
		t.Fatalf("local server args = %#v, want %#v", server.Args, want)
	}

	server = globalServer(root, true)
	want = []string{"serve", "--workspace", root}
	if !equalStrings(server.Args, want) {
		t.Fatalf("workspace server args = %#v, want %#v", server.Args, want)
	}
}

func TestPlanOmitsToolProfileServerArg(t *testing.T) {
	root := t.TempDir()

	results, err := Plan(root, Options{Clients: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("Plan returned %d results, want 1", len(results))
	}
	if strings.Contains(results[0].Detail, "--tool-profile") {
		t.Fatalf("plan detail must not include --tool-profile: %q", results[0].Detail)
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
