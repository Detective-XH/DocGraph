package docformat

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}

func TestReadFileCapped(t *testing.T) {
	t.Run("under cap reads fully", func(t *testing.T) {
		path := writeTemp(t, 100)
		data, err := ReadFileCapped(path, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(data) != 100 {
			t.Fatalf("len = %d; want 100", len(data))
		}
	})

	t.Run("at cap reads fully", func(t *testing.T) {
		path := writeTemp(t, 100)
		data, err := ReadFileCapped(path, 100)
		if err != nil {
			t.Fatalf("unexpected error at exact cap: %v", err)
		}
		if len(data) != 100 {
			t.Fatalf("len = %d; want 100", len(data))
		}
	})

	t.Run("over cap is rejected", func(t *testing.T) {
		path := writeTemp(t, 101)
		if _, err := ReadFileCapped(path, 100); err == nil {
			t.Fatal("expected error for file exceeding cap, got nil")
		}
	})

	t.Run("non-positive limit reads uncapped", func(t *testing.T) {
		path := writeTemp(t, 1000)
		data, err := ReadFileCapped(path, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(data) != 1000 {
			t.Fatalf("len = %d; want 1000", len(data))
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		if _, err := ReadFileCapped(filepath.Join(t.TempDir(), "nope"), 100); err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})
}
