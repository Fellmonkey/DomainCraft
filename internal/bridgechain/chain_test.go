package bridgechain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

func writeBridge(t *testing.T, dir, extends string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "name: " + filepath.Base(dir) + "\n"
	if extends != "" {
		content += "extends: " + filepath.ToSlash(extends) + "\n"
	}
	path := filepath.Join(dir, "bridge.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return dir
}

func testResolver() *bridge.Resolver {
	return bridge.NewResolver(bridge.Default())
}

func TestWalkResolvesExtendsChainTopFirst(t *testing.T) {
	root := t.TempDir()
	base := writeBridge(t, filepath.Join(root, "base"), "")
	mid := writeBridge(t, filepath.Join(root, "mid"), base)
	top := writeBridge(t, filepath.Join(root, "top"), mid)

	chain, err := Walk(top, top, testResolver(), nil)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{top, mid, base}
	if len(chain) != len(want) {
		t.Fatalf("chain = %v, want %v", chain, want)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Errorf("chain[%d] = %q, want %q", i, chain[i], want[i])
		}
		if !filepath.IsAbs(chain[i]) {
			t.Errorf("chain[%d] = %q, want an absolute path", i, chain[i])
		}
	}
}

func TestWalkDetectsCycle(t *testing.T) {
	root := t.TempDir()
	a := writeBridge(t, filepath.Join(root, "a"), "")
	b := writeBridge(t, filepath.Join(root, "b"), a)
	writeBridge(t, a, b) // a → b → a

	_, err := Walk(a, a, testResolver(), nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want a cycle error", err)
	}
}

func TestWalkFailsWhenAbsFails(t *testing.T) {
	root := t.TempDir()
	base := writeBridge(t, filepath.Join(root, "base"), "")

	original := absPathFn
	t.Cleanup(func() { absPathFn = original })
	absPathFn = func(string) (string, error) { return "", os.ErrPermission }

	_, err := Walk(base, root, testResolver(), nil)
	if err == nil {
		t.Fatal("Walk accepted a bridge path it could not make absolute")
	}
	if !strings.Contains(err.Error(), "resolve absolute bridge path") {
		t.Errorf("err = %v, want the absolute-path failure context", err)
	}
}

func TestWalkMissingLocalPathIsHardError(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-bridge")

	_, err := Walk(missing, root, testResolver(), nil)
	if err == nil {
		t.Fatal("expected an error for a missing local bridge")
	}
	if !strings.Contains(err.Error(), "no such local path") {
		t.Errorf("err = %v, want the local-path failure text", err)
	}
	if strings.Contains(err.Error(), "clone") {
		t.Errorf("err = %v, must not attempt a clone for a path-like ref", err)
	}
	if !strings.HasPrefix(err.Error(), "resolve bridge `extends:") {
		t.Errorf("err = %v, want the chain resolution context", err)
	}
}
