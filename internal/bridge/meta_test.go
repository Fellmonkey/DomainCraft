package bridge

import (
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

func TestMetaRoundTrip(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := cacheEntry("test-bridge", "owner/bridge")

	// Nothing cached yet -> no meta, not cached.
	if _, err := ReadMeta(entry); err != nil {
		t.Fatalf("ReadMeta on missing meta: %v", err)
	}

	now := time.Now()
	meta := &CacheMeta{
		ID:            entry.ID,
		Source:        entry.GitHub,
		Version:       "1.0.0",
		Commit:        "abc123",
		Branch:        "main",
		ClonedAt:      now,
		LastCheckedAt: now,
	}
	if err := WriteMeta(entry, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := ReadMeta(entry)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got.Version != "1.0.0" || got.Commit != "abc123" || got.Source != "owner/bridge" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.ClonedAt.Equal(now) {
		t.Fatalf("ClonedAt not preserved: got %v want %v", got, now)
	}
}
