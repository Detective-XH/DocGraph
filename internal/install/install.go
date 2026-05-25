package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	clientClaude   = "claude"
	clientCodex    = "codex"
	clientHermes   = "hermes"
	clientOpenCode = "opencode"
)

// Options controls non-interactive MCP client configuration.
// Clients accepts "auto", "all", or a comma-separated client list.
// Scope applies only to the Claude client: "user" invokes "claude mcp add --scope user"
// to register in ~/.claude.json; any other value writes a project-local .mcp.json.
type Options struct {
	Clients   string
	Workspace bool
	Scope     string
}

// Result records one client configuration file updated by the installer.
type Result struct {
	Client string
	Path   string
}

type mcpServer struct {
	Command string   `json:"command" yaml:"command"`
	Args    []string `json:"args" yaml:"args"`
}

// Apply writes DocGraph MCP configuration for the selected clients.
// The installer is intentionally non-interactive so it can be used from
// scripted setup flows and tested without terminal prompts.
func Apply(root string, opts Options) ([]Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	clients, err := resolveClients(absRoot, opts.Clients)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, client := range clients {
		var path string
		var err error
		switch client {
		case clientClaude:
			if opts.Scope == "user" {
				err = invokeClaudeMCPAdd(absRoot, opts.Workspace)
				if err == nil {
					path = "~/.claude.json"
				}
			} else {
				path = filepath.Join(absRoot, ".mcp.json")
				err = writeJSONMCP(path, localServer(absRoot, opts.Workspace))
			}
		case clientCodex:
			path, err = codexConfigPath()
			if err == nil {
				err = writeCodexTOML(path, globalServer(absRoot, opts.Workspace))
			}
		case clientHermes:
			path, err = hermesConfigPath()
			if err == nil {
				err = writeHermesYAML(path, globalServer(absRoot, opts.Workspace))
			}
		case clientOpenCode:
			path, err = openCodeConfigPath(absRoot)
			if err == nil {
				err = writeJSONMCP(path, globalServer(absRoot, opts.Workspace))
			}
		default:
			err = fmt.Errorf("unsupported client %q", client)
		}
		if err != nil {
			return nil, err
		}
		results = append(results, Result{Client: client, Path: path})
	}
	return results, nil
}

func resolveClients(root, raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "auto"
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "auto" {
		return detectClients(root), nil
	}
	if raw == "all" {
		return []string{clientClaude, clientCodex, clientHermes, clientOpenCode}, nil
	}

	seen := map[string]bool{}
	var clients []string
	for _, part := range strings.Split(raw, ",") {
		client := strings.TrimSpace(part)
		if client == "" {
			continue
		}
		if !isSupportedClient(client) {
			return nil, fmt.Errorf("unsupported client %q", client)
		}
		if !seen[client] {
			seen[client] = true
			clients = append(clients, client)
		}
	}
	if len(clients) == 0 {
		return nil, errors.New("no clients selected")
	}
	return clients, nil
}

func detectClients(root string) []string {
	seen := map[string]bool{clientClaude: true}
	clients := []string{clientClaude}

	if path, err := codexConfigPath(); err == nil && pathExistsOrParentExists(path) {
		clients = append(clients, clientCodex)
		seen[clientCodex] = true
	}
	if path, err := hermesConfigPath(); err == nil && pathExistsOrParentExists(path) {
		clients = append(clients, clientHermes)
		seen[clientHermes] = true
	}
	if path, err := openCodeConfigPath(root); err == nil && pathExistsOrParentExists(path) && !seen[clientOpenCode] {
		clients = append(clients, clientOpenCode)
	}

	return clients
}

func isSupportedClient(client string) bool {
	switch client {
	case clientClaude, clientCodex, clientHermes, clientOpenCode:
		return true
	default:
		return false
	}
}

func localServer(root string, workspace bool) mcpServer {
	if workspace {
		return globalServer(root, true)
	}
	return mcpServer{Command: "docgraph", Args: []string{"serve", "--path", "."}}
}

func globalServer(root string, workspace bool) mcpServer {
	mode := "--path"
	if workspace {
		mode = "--workspace"
	}
	return mcpServer{Command: "docgraph", Args: []string{"serve", mode, root}}
}

func writeJSONMCP(path string, server mcpServer) error {
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		doc["mcpServers"] = servers
	}
	servers["docgraph"] = server

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data, 0o644)
}

func writeCodexTOML(path string, server mcpServer) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	block := codexBlock(server)
	next := replaceManagedBlock(string(data), "# BEGIN DocGraph MCP", "# END DocGraph MCP", block)
	return writeFile(path, []byte(next), 0o644)
}

func codexBlock(server mcpServer) string {
	quotedArgs := make([]string, 0, len(server.Args))
	for _, arg := range server.Args {
		quotedArgs = append(quotedArgs, fmt.Sprintf("%q", arg))
	}
	return fmt.Sprintf(`# BEGIN DocGraph MCP
[mcp_servers.docgraph]
command = %q
args = [%s]
# END DocGraph MCP
`, server.Command, strings.Join(quotedArgs, ", "))
}

func writeHermesYAML(path string, server mcpServer) error {
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	servers, _ := doc["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		doc["mcp_servers"] = servers
	}
	servers["docgraph"] = server

	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return writeFile(path, data, 0o644)
}

func replaceManagedBlock(existing, begin, end, block string) string {
	existing = strings.ReplaceAll(existing, "\r\n", "\n")
	start := strings.Index(existing, begin)
	if start >= 0 {
		stopRel := strings.Index(existing[start:], end)
		if stopRel >= 0 {
			stop := start + stopRel + len(end)
			if stop < len(existing) && existing[stop] == '\n' {
				stop++
			}
			next := existing[:start] + block + existing[stop:]
			if !strings.HasSuffix(next, "\n") {
				next += "\n"
			}
			return next
		}
	}
	if strings.TrimSpace(existing) == "" {
		return block
	}
	sep := "\n"
	if !strings.HasSuffix(existing, "\n") {
		sep = "\n\n"
	}
	return existing + sep + block
}

func codexConfigPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return filepath.Join(dir, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func hermesConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hermes", "config.yaml"), nil
}

func openCodeConfigPath(root string) (string, error) {
	for _, name := range []string{"opencode.json", ".opencode.json"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "opencode", "opencode.json"), nil
}

func pathExistsOrParentExists(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		return true
	}
	return false
}

// invokeClaudeMCPAdd registers DocGraph with Claude Code at user scope by
// running "claude mcp add --scope user". This avoids direct writes to
// ~/.claude.json, which contains auth tokens and has a complex structure.
func invokeClaudeMCPAdd(root string, workspace bool) error {
	absBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate docgraph binary: %w", err)
	}
	server := globalServer(root, workspace)
	cmdArgs := []string{"mcp", "add", "--scope", "user", "docgraph", "--", absBin}
	cmdArgs = append(cmdArgs, server.Args...)
	cmd := exec.Command("claude", cmdArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude mcp add failed: %w\n(Is 'claude' on your PATH? Run: claude mcp list to verify)", err)
	}
	return nil
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// IsClaudeResult reports whether r is a Claude Code installation result.
func IsClaudeResult(r Result) bool { return r.Client == clientClaude }

// Clients returns the supported client names in stable display order.
func Clients() []string {
	clients := []string{clientClaude, clientCodex, clientHermes, clientOpenCode}
	sort.Strings(clients)
	return clients
}
