package bridge

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	domaincraft "github.com/DomainCraft/DomainCraft"
	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// TestTryMaterializeEmbedded verifies that an embedded bridge can be
// materialized into the cache directory (the offline fallback path).
func TestTryMaterializeEmbedded(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })
	entry := Default().ByID("csharp-core")
	if entry == nil {
		t.Fatal("csharp-core not in registry")
	}
	ok, err := TryMaterializeEmbedded(*entry, nil)
	if err != nil {
		t.Fatalf("TryMaterializeEmbedded: %v", err)
	}
	if !ok {
		t.Fatal("expected embedded materialization to succeed")
	}
	if _, err := os.Stat(filepath.Join(CachePath(*entry), "bridge.yaml")); err != nil {
		t.Fatalf("materialized bridge missing bridge.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(CachePath(*entry), "templates")); err != nil {
		t.Fatalf("materialized bridge missing templates dir: %v", err)
	}
}

// F27: the embedded tree path has one spelling for all lookups.
func TestEmbeddedBridgeRel(t *testing.T) {
	if got := embeddedBridgeRel("csharp/core"); got != "bridges/csharp/core" {
		t.Errorf("embeddedBridgeRel = %q, want bridges/csharp/core", got)
	}
	if got := embeddedBridgeRel(filepath.FromSlash("csharp/core")); got != "bridges/csharp/core" {
		t.Errorf("embeddedBridgeRel with OS separators = %q, want bridges/csharp/core", got)
	}
}

// TestEmbeddedContainsRegistryBridges guards against drift between the embedded
// bridge tree (what ships in the binary) and the registry mapping. Every
// registered, non-deprecated bridge must exist embedded; the deprecated
// csharp-restful monolith is intentionally not bundled.
func TestEmbeddedContainsRegistryBridges(t *testing.T) {
	reg := Default()
	for _, e := range reg.All() {
		if e.Path == "" || e.Deprecated {
			continue
		}
		rel := "bridges/" + e.Path + "/bridge.yaml"
		if _, err := domaincraft.Bridges.ReadFile(rel); err != nil {
			t.Errorf("embedded tree missing bridge %q (want %s): %v", e.ID, rel, err)
		}
	}
}

// TestEmbeddedBridgesMatchTree compares the embedded tree with the source tree.
// Helper templates are named helpers.*.tmpl, so //go:embed bridges picks them up
// along with every other file. The two sets must stay identical, or an installed
// binary cannot render.
func TestEmbeddedBridgesMatchTree(t *testing.T) {
	root := filepath.Join("..", "..", "bridges")
	if _, err := os.Stat(root); err != nil {
		if os.Getenv("DOMAINCRAFT_LOCAL_DEV") != "" {
			t.Skipf("no bundled bridges tree at %s (DOMAINCRAFT_LOCAL_DEV set): %v", root, err)
		}
		t.Fatalf("bundled bridges tree missing at %s: %v", root, err)
	}

	embedded := make(map[string]bool)
	if err := fs.WalkDir(domaincraft.Bridges, "bridges", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Generated TCK output and snapshots are runtime artifacts: they
			// may exist on disk but not in the compiled embed (or vice versa).
			switch d.Name() {
			case "bin", "obj", "node_modules", ".git", ".domaincraft":
				return fs.SkipDir
			case "generated":
				if filepath.Base(filepath.Dir(path)) == "tck" {
					return fs.SkipDir
				}
			}
			return nil
		}
		embedded[path] = true
		return nil
	}); err != nil {
		t.Fatalf("walk embedded bridges: %v", err)
	}

	onDisk := make(map[string]bool)
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Local build artifacts are not part of the source tree and go:embed
			// does not include them either. TCK output and .domaincraft snapshots
			// are runtime artifacts for the same reason.
			switch d.Name() {
			case "bin", "obj", "node_modules", ".git", ".domaincraft":
				return filepath.SkipDir
			case "generated":
				if filepath.Base(filepath.Dir(path)) == "tck" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		onDisk["bridges/"+filepath.ToSlash(rel)] = true
		return nil
	}); err != nil {
		t.Fatalf("walk source bridges: %v", err)
	}

	for _, path := range slices.Sorted(maps.Keys(onDisk)) {
		if !embedded[path] {
			t.Errorf("embedded tree missing %s: check go:embed name exclusions", path)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(embedded)) {
		if !onDisk[path] {
			t.Errorf("embedded tree has %s but the source tree does not", path)
		}
	}
}
