package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

func TestCmdPackEnableDisableCodeDoc(t *testing.T) {
	projectDir := t.TempDir()
	writeTestFile(t, filepath.Join(projectDir, "service.go"), `package service

// Handler handles pack-enabled requests.
func Handler() {}
`)

	cmdPack([]string{"enable", "code_doc", projectDir})

	st := openPackTestStore(t, projectDir)
	enabled, err := st.IsPackEnabled(codeDocPackID)
	if err != nil {
		t.Fatalf("IsPackEnabled: %v", err)
	}
	if !enabled {
		t.Fatal("code_doc should be enabled")
	}
	nodes, err := st.GetNodesByFile("service.go")
	if err != nil {
		t.Fatalf("GetNodesByFile after enable: %v", err)
	}
	if len(nodes) == 0 || nodes[0].Kind != "code_file" {
		t.Fatalf("expected indexed code_file after enable, got %#v", nodes)
	}
	st.Close()

	cmdPack([]string{"disable", "code-doc", projectDir})

	st = openPackTestStore(t, projectDir)
	enabled, err = st.IsPackEnabled(codeDocPackID)
	if err != nil {
		t.Fatalf("IsPackEnabled after disable: %v", err)
	}
	if enabled {
		t.Fatal("code_doc should be disabled")
	}
	nodes, err = st.GetNodesByFile("service.go")
	if err != nil {
		t.Fatalf("GetNodesByFile after disable: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("disable should remove indexed code_doc nodes, got %#v", nodes)
	}
	st.Close()
}

func TestCmdPackEnableCodeDocWorkspace(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(projectDir, "service.go"), `package service

// Worker handles workspace pack-enabled requests.
func Worker() {}
`)

	cmdPack([]string{"enable", "--workspace", "code_doc", root})

	st := openPackTestStore(t, projectDir)
	defer st.Close()
	enabled, err := st.IsPackEnabled(codeDocPackID)
	if err != nil {
		t.Fatalf("IsPackEnabled workspace: %v", err)
	}
	if !enabled {
		t.Fatal("workspace code_doc should be enabled")
	}
	nodes, err := st.GetNodesByFile("service.go")
	if err != nil {
		t.Fatalf("GetNodesByFile workspace: %v", err)
	}
	if len(nodes) == 0 || nodes[0].Kind != "code_file" {
		t.Fatalf("expected workspace code_file node, got %#v", nodes)
	}
}

func openPackTestStore(t *testing.T, projectDir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(projectDir, ".docgraph", "docgraph.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
