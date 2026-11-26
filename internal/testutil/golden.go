package testutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CheckGolden compares got with testdata/<name>. CRLF and one trailing newline
// are normalized so a golden file checks out identically on Windows and Linux.
// Pass update=true from a package-local -update flag to rewrite the file
// deliberately; a mismatch otherwise fails the test with both values.
func CheckGolden(t *testing.T, name, got string, update bool) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if update {
		if err := os.MkdirAll("testdata", 0o755); err != nil { //nolint:gosec // golden fixtures must stay readable in the repo
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil { //nolint:gosec // committed golden files use the repo's 0644 convention
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // name is a package-local testdata filename, not user input
	if err != nil {
		t.Fatalf("read golden %s (run this package's tests with -update to create): %v", path, err)
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	// CheckGolden writes exactly one trailing newline; trimming only that one
	// keeps an accidental extra blank line in a golden visible.
	if want := strings.TrimSuffix(string(data), "\n"); want != got {
		t.Errorf("output differs from golden %s\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}

// HashLines digests canonical lines into a stable, host-independent value.
// Callers sort and format their own lines before hashing.
func HashLines(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
