package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
)

// MetaFileName is the cache-metadata filename stored inside each cached bridge.
// It records where a bridge was downloaded from and which version/commit it is
// on, so the CLI can detect when a newer upstream version is available without
// re-cloning or re-reading the whole checkout.
const MetaFileName = ".domaincraft-meta.json"

// CacheMeta records the origin and version of a cached bridge checkout.
type CacheMeta struct {
	ID      string `json:"id"`      // bridge registry ID, e.g. "csharp-restful"
	Source  string `json:"source"`  // GitHub "owner/repo"
	Version string `json:"version"` // bridge.yaml version at download/update time
	Commit  string `json:"commit"`  // git HEAD of the local checkout
	Branch  string `json:"branch"`  // upstream default branch
	// Pin is the exact ref requested by `owner/repo@ref`. It is empty for
	// registry IDs and unpinned shorthands. Old metadata has no field, which
	// decodes to "" and keeps those caches backwards compatible.
	Pin string `json:"pin,omitempty"`
	// ClonedAt marks when the bridge was first downloaded.
	ClonedAt time.Time `json:"cloned_at"`
	// LastCheckedAt is updated every time the remote is actually contacted, so
	// the freshness gate (EnsureOptions.Interval) can avoid a network call on
	// every invocation.
	LastCheckedAt time.Time `json:"last_checked_at"`
}

// MetaPath returns the cache-metadata file path for a bridge entry.
func MetaPath(entry RegistryEntry) string {
	return filepath.Join(CachePath(entry), MetaFileName)
}

// ReadMeta loads cache metadata for a bridge. It returns (nil, nil) when no
// metadata file exists (e.g. a bridge cached before metadata was introduced).
func ReadMeta(entry RegistryEntry) (*CacheMeta, error) {
	var meta CacheMeta
	if err := fsutil.ReadJSON(MetaPath(entry), &meta); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &meta, nil
}

// WriteMeta persists cache metadata atomically (write to a temp file, then
// rename) so a crash mid-write never leaves a corrupt metadata file behind.
func WriteMeta(entry RegistryEntry, meta *CacheMeta) error {
	if meta == nil {
		return fmt.Errorf("cannot write nil cache metadata")
	}
	path := MetaPath(entry)
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // bridge cache dirs must stay readable across CLI runs
		return err
	}
	root, rel, err := fsutil.OpenRootFor(parent, path)
	if err != nil {
		return fmt.Errorf("open cache root for %q: %w", entry.ID, err)
	}
	defer func() { _ = root.Close() }()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache metadata for %q: %w", entry.ID, err)
	}
	return fsutil.AtomicWriteRoot(root, rel, data)
}

// ensureMeta returns the cache metadata for a cached bridge, synthesizing it
// from the on-disk checkout when the metadata file is missing.
func ensureMeta(entry RegistryEntry) (*CacheMeta, error) {
	meta, err := ReadMeta(entry)
	if err != nil {
		return nil, err
	}
	if meta != nil {
		return meta, nil
	}
	// Bridges cached before metadata existed: build it from the checkout.
	if !IsCached(entry) {
		return nil, nil
	}
	return newMetaFromCache(entry), nil
}

// modifyMeta performs one read-modify-write of a bridge's cache metadata.
// A bridge cached before metadata existed is synthesized first, so callers
// never have to repeat the ensure/mutate/write sequence.
func modifyMeta(entry RegistryEntry, mutate func(*CacheMeta)) error {
	meta, err := ensureMeta(entry)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = newMetaFromCache(entry)
	}
	mutate(meta)
	return WriteMeta(entry, meta)
}

// newMetaFromCache builds metadata from the current on-disk checkout. It is
// best-effort: missing git or an unreadable bridge.yaml degrade gracefully.
func newMetaFromCache(entry RegistryEntry) *CacheMeta {
	now := time.Now()
	return &CacheMeta{
		ID:            entry.ID,
		Source:        entry.GitHub,
		Version:       readBridgeVersion(entry),
		Commit:        gitHead(entry),
		Branch:        gitBranch(entry),
		ClonedAt:      now,
		LastCheckedAt: now,
	}
}

// readBridgeVersion parses the `version:` field out of a cached bridge.yaml.
// Returns "" when the field is absent or the file cannot be read.
func readBridgeVersion(entry RegistryEntry) string {
	manifest, err := LoadManifest(CachePath(entry))
	if err != nil {
		return ""
	}
	return manifest.Version
}

// gitHead returns the current git HEAD commit of the cached checkout, or ""
// when it cannot be read. Callers that must tell "no commit" from "git failed"
// use gitHeadResult; a failed probe must not be mistaken for an empty HEAD.
func gitHead(entry RegistryEntry) string {
	head, _ := gitHeadResult(entry)
	return head
}

// gitHeadResult returns the cached checkout's HEAD commit and reports git
// failures separately. An error means the local commit is unknown — callers
// must not fall back to a stale metadata commit and compare it with the remote.
func gitHeadResult(entry RegistryEntry) (string, error) {
	out, err := runGit(CachePath(entry), "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read local bridge HEAD in %q: %w", CachePath(entry), err)
	}
	return out, nil
}

// gitBranch returns the current branch of the cached checkout, or "".
func gitBranch(entry RegistryEntry) string {
	out, err := runGit(CachePath(entry), "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}
