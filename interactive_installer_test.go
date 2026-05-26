package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdInitDryRunDoesNotWriteSetupArtifacts(t *testing.T) {
	projectDir := t.TempDir()

	cmdInit([]string{"--dry-run", "--install-clients", "claude", "--with-skills", projectDir})

	for _, path := range []string{
		filepath.Join(projectDir, ".docgraph"),
		filepath.Join(projectDir, ".docgraphignore"),
		filepath.Join(projectDir, ".gitignore"),
		filepath.Join(projectDir, ".mcp.json"),
		filepath.Join(projectDir, ".claude"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s", path)
		}
	}
}

func TestCmdInstallDryRunDoesNotWriteClientConfigs(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)

	cmdInstall([]string{"--dry-run", "--clients", "claude,codex", projectDir})

	for _, path := range []string{
		filepath.Join(projectDir, ".mcp.json"),
		filepath.Join(codexHome, "config.toml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s", path)
		}
	}
}

func TestConfirmParsesYesAndDefaultsNo(t *testing.T) {
	var out bytes.Buffer
	if !confirm(bytes.NewBufferString("yes\n"), &out, "Apply?") {
		t.Fatal("yes should confirm")
	}
	if confirm(bytes.NewBufferString("\n"), &out, "Apply?") {
		t.Fatal("empty answer should decline")
	}
}
