package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// A corrupt metadata file used to be ignored: the pin was lost and the update
// silently moved a pinned checkout to the default branch.
func TestApplyUpdateRejectsCorruptMetadata(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	entry := cacheEntry("corrupt-meta", "DomainCraft/test")
	dir := CachePath(entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MetaFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyUpdate(entry, &Update{})
	if err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("ApplyUpdate = %v, want a metadata error", err)
	}
}
