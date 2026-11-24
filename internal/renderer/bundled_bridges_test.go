package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

// TestBundledBridgesLoad certifies that every bridge bundled in the monorepo
// (`bridges/**`) loads and passes the contract gate. It is the first half of
// the plan's contract test: it catches a bridge that stops loading — e.g. one
// that starts depending on a newer contract — long before it ships. The
// byte-identical output half is a follow-up golden test.
func TestBundledBridgesLoad(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "bridges")
	if _, err := os.Stat(root); err != nil {
		// In a standalone checkout the monorepo tree is absent. Skipping is only
		// acceptable when the developer explicitly opts into that local-dev mode;
		// otherwise a missing tree would silently certify zero bridges in CI.
		if os.Getenv("DOMAINCRAFT_LOCAL_DEV") != "" {
			t.Skipf("no bundled bridges tree at %s (DOMAINCRAFT_LOCAL_DEV set): %v", root, err)
		}
		t.Fatalf("bundled bridges tree missing at %s: %v", root, err)
	}

	var found int
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "bridge.yaml" {
			return nil
		}
		found++
		dir := filepath.Dir(path)
		r, lerr := New(dir, logger.New())
		if lerr != nil {
			t.Errorf("bundled bridge %s failed to load: %v",
				strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), root+"/"), lerr)
			return nil
		}
		if r == nil {
			t.Errorf("bundled bridge %s returned a nil renderer", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk bundled bridges: %v", walkErr)
	}
	if found == 0 {
		t.Fatal("no bundled bridges found — expected the monorepo bridges tree")
	}
	t.Logf("loaded %d bundled bridges", found)
}
