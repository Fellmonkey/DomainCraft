package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// shallowPinnedCache builds the state ApplyUpdate leaves after its fallback
// re-clone: a --depth 1 checkout of the default branch (v2.0.0), with a pin
// recorded in metadata that the shallow checkout does not necessarily contain.
func shallowPinnedCache(t *testing.T, pin string) (RegistryEntry, string) {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	remote, src := newRemoteWithBridge(t, root, "main", "")
	testGit(t, src, "tag", "v1.0.0")
	// file:// makes the later shallow fetch use the real transport instead of
	// a local-path clone, which ignores --depth.
	remoteURL := "file:///" + filepath.ToSlash(remote)
	testGit(t, src, "remote", "set-url", "origin", remoteURL)
	testGit(t, src, "push", "-q", "-u", "origin", "main")
	testGit(t, src, "push", "-q", "origin", "v1.0.0")

	writeBridgeYAML(t, src, "2.0.0")
	testGit(t, src, "add", ".")
	testGit(t, src, "commit", "-q", "-m", "bump version")
	testGit(t, src, "push", "-q", "origin", "main")

	entry := cacheEntry("pin-recovery", "owner/pin-recovery")
	dir := CachePath(entry)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "clone", "-q", "--depth", "1", remoteURL, dir)
	if err := WriteMeta(entry, &CacheMeta{
		ID: entry.ID, Source: entry.GitHub, Version: "2.0.0",
		Commit: testGit(t, dir, "rev-parse", "HEAD"), Branch: "main", Pin: pin,
		ClonedAt: time.Now(), LastCheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return entry, dir
}

// F23: a shallow fallback clone does not carry an arbitrary pinned ref. The
// update may fail, but it must not leave metadata claiming a pin the checkout
// does not hold — that would disable every later freshness check.
func TestFinishUpdateDropsPinWhenRefCannotBeFetched(t *testing.T) {
	entry, dir := shallowPinnedCache(t, "missing-pin")

	version, err := finishUpdate(entry, "main", "missing-pin")
	if err == nil || !strings.Contains(err.Error(), "restore pinned ref") {
		t.Fatalf("finishUpdate error = %v, want a failed pin restore", err)
	}
	if version != "" {
		t.Fatalf("finishUpdate version = %q, want empty on failure", version)
	}
	meta, err := ReadMeta(entry)
	if err != nil || meta == nil {
		t.Fatalf("ReadMeta: %+v, %v", meta, err)
	}
	if meta.Pin != "" {
		t.Fatalf("stale pin %q kept after a failed restore", meta.Pin)
	}
	if meta.Commit != testGit(t, dir, "rev-parse", "HEAD") {
		t.Fatalf("metadata commit %q does not match the checkout", meta.Commit)
	}
	data, err := os.ReadFile(filepath.Join(dir, "bridge.yaml"))
	if err != nil || !strings.Contains(string(data), `version: "2.0.0"`) {
		t.Fatalf("branch checkout lost after failed pin restore: %q, %v", data, err)
	}
}

// F23: when the ref exists upstream, fetch it explicitly and land on the pin
// instead of silently serving the default branch.
func TestFinishUpdateFetchesMissingPinnedRef(t *testing.T) {
	entry, dir := shallowPinnedCache(t, "v1.0.0")
	if tags := testGit(t, dir, "tag", "-l"); strings.Contains(tags, "v1.0.0") {
		t.Skip("this git version included the tag in a shallow clone; nothing to recover")
	}

	version, err := finishUpdate(entry, "main", "v1.0.0")
	if err != nil {
		t.Fatalf("finishUpdate: %v", err)
	}
	if version != "1.0.0" {
		t.Fatalf("version = %q, want the pinned 1.0.0", version)
	}
	meta, err := ReadMeta(entry)
	if err != nil || meta == nil {
		t.Fatalf("ReadMeta: %+v, %v", meta, err)
	}
	if meta.Pin != "v1.0.0" {
		t.Fatalf("pin = %q, want v1.0.0", meta.Pin)
	}
	if meta.Commit != testGit(t, dir, "rev-parse", "HEAD") {
		t.Fatalf("metadata commit %q does not match the pinned checkout", meta.Commit)
	}
	data, err := os.ReadFile(filepath.Join(dir, "bridge.yaml"))
	if err != nil || !strings.Contains(string(data), `version: "1.0.0"`) {
		t.Fatalf("checkout is not on the pinned version: %q, %v", data, err)
	}
}
