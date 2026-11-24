package bridge

import (
	"os"
	"path/filepath"
	"testing"

	domaincraft "github.com/DomainCraft/DomainCraft"
	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// TestEmbeddedContainsRegistryBridges guards against drift between the embedded
// bridge tree (what ships in the binary) and the registry mapping. Every
// registered, non-deprecated bridge must exist embedded; the deprecated
// csharp-restful monolith is intentionally not bundled.
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
