package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestInitDryRunOmitsToolProfileServerArg(t *testing.T) {
	projectDir := t.TempDir()

	output := captureStderr(t, func() {
		cmdInit([]string{"--dry-run", "--install-clients", "claude", projectDir})
	})

	if strings.Contains(output, "--tool-profile") {
		t.Fatalf("init dry-run must not include --tool-profile in server args:\n%s", output)
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

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data)
}
