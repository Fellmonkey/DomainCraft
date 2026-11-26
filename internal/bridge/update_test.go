package bridge

import (
	"os"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// TestCheckAndApplyUpdate exercises the full freshness flow: GitHub API
// detection (faked offline) plus a real git fetch+reset apply against a local
// bare remote, so it doesn't depend on the network.
func TestCheckAndApplyUpdate(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	// Bare remote acting as the bridge's origin, with a working source repo.
	remote, src := newRemoteWithBridge(t, root, "main", "")

	// "Download" the bridge into our cache just like EnsureBridge would.
	entry := RegistryEntry{ID: "test-bridge", GitHub: "owner/test-bridge"}
	cacheDir := CachePath(entry)
	testGit(t, root, "clone", "-q", remote, cacheDir)
	if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
		t.Fatalf("write meta after clone: %v", err)
	}

	// Fake the GitHub API: report the current head of the local bare remote.
	stub := stubGitHubAPI(t, testGit(t, remote, "rev-parse", "HEAD"), "")

	// No update yet: local checkout already at the remote tip.
	upd, err := CheckForUpdate(entry, 0)
	if err != nil {
		t.Fatalf("CheckForUpdate (fresh): %v", err)
	}
	if upd != nil {
		t.Fatalf("expected no update, got %+v", upd)
	}

	// Freeze the last-checked timestamp so the freshness gate is exercised.
	meta, err := ReadMeta(entry)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	meta.LastCheckedAt = time.Now()
	if err := WriteMeta(entry, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Within the TTL the remote must NOT be contacted -> nil update.
	if upd, err := CheckForUpdate(entry, time.Hour); err != nil || upd != nil {
		t.Fatalf("expected TTL gate to skip check (upd=%+v err=%v)", upd, err)
	}

	// Publish a newer version upstream.
	writeBridgeYAML(t, src, "2.0.0")
	testGit(t, src, "add", ".")
	testGit(t, src, "commit", "-q", "-m", "bump version")
	testGit(t, src, "push", "-q", "origin", "main")
	stub.sha = testGit(t, remote, "rev-parse", "HEAD")

	// Now an update is detected.
	upd, err = CheckForUpdate(entry, 0)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if upd == nil {
		t.Fatalf("expected an update after bumping the remote version")
	}
	if upd.LocalVersion != "1.0.0" {
		t.Fatalf("wrong local version: %q", upd.LocalVersion)
	}

	// Applying the update brings the checkout to v2.0.0.
	newVer, err := ApplyUpdate(entry, upd)
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if newVer != "2.0.0" {
		t.Fatalf("expected update to 2.0.0, got %q", newVer)
	}

	// The local HEAD now matches the remote tip -> no more update.
	if upd2, err := CheckForUpdate(entry, 0); err != nil || upd2 != nil {
		t.Fatalf("expected no update after applying (upd=%+v err=%v)", upd2, err)
	}
}

func TestEnsureMetaSynthesizesForLegacyCache(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := RegistryEntry{ID: "legacy-bridge", GitHub: "owner/legacy-bridge"}
	cacheDir := CachePath(entry)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, cacheDir, "main")

	// No metadata file exists (legacy cache), but the checkout is usable.
	meta, err := ensureMeta(entry)
	if err != nil {
		t.Fatalf("ensureMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("expected metadata to be synthesized for a cached bridge")
	}
	if meta.Version != "1.0.0" {
		t.Fatalf("expected synthesized version 1.0.0, got %q", meta.Version)
	}
	if meta.Commit == "" || meta.Branch != "main" {
		t.Fatalf("expected git-derived metadata, got %+v", meta)
	}
}

// TestCheckForUpdateSkippedWithoutGitCheckout pins the F22 rule: a cache whose
// HEAD cannot be read has no trustworthy local commit. Even when the GitHub API
// reports a different SHA, that stale metadata commit must not produce a false
// "update available" (the same outcome as an offline probe).
func TestCheckForUpdateSkippedWithoutGitCheckout(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := RegistryEntry{ID: "github-bridge", GitHub: "owner/github-bridge"}
	cacheDir := CachePath(entry)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBridgeYAML(t, cacheDir, "1.0.0")

	// No .git in the checkout, but metadata records the local commit.
	meta := &CacheMeta{
		ID:            entry.ID,
		Source:        entry.GitHub,
		Version:       "1.0.0",
		Commit:        "old-local-commit",
		Branch:        "main",
		ClonedAt:      time.Now().Add(-24 * time.Hour),
		LastCheckedAt: time.Now().Add(-24 * time.Hour),
	}
	if err := WriteMeta(entry, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Fake the GitHub API responses.
	stubGitHubAPI(t, "new-remote-commit", "")

	upd, err := CheckForUpdate(entry, 0)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if upd != nil {
		t.Fatalf("a cache without a readable HEAD must not offer an update: %+v", upd)
	}
	after, err := ReadMeta(entry)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if after.Commit != "old-local-commit" {
		t.Fatalf("stale commit was overwritten despite git failing: %q", after.Commit)
	}
	if after.LastCheckedAt.Equal(meta.LastCheckedAt) {
		t.Fatal("CheckForUpdate stopped recording the probe attempt")
	}
}
