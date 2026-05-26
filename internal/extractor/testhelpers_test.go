package extractor

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
)

// testdataDir returns the absolute path to the testdata/multiformat directory.
// It uses runtime.Caller so it works regardless of the working directory from
// which `go test` is invoked.
func testdataDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("testdataDir: could not determine source file path via runtime.Caller")
	}
	// file = .../internal/extractor/testhelpers_test.go
	// We want .../internal/extractor/testdata/multiformat
	dir := filepath.Join(filepath.Dir(file), "testdata", "multiformat")
	return filepath.Clean(dir)
}

// sha256hex returns the hex-encoded SHA-256 digest of data.
func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
