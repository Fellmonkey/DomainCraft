package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
)

// DefaultCacheTTL is how long a resolved package version is trusted before the
// registry is consulted again. Versions change rarely, so 24h keeps generation
// offline-friendly while still picking up real version bumps.
const DefaultCacheTTL = 24 * time.Hour

// CacheFileName is the file storing resolved package versions inside a bridge's
// cache directory.
const CacheFileName = "packages.json"

// versionEntry is one cached package version.
type versionEntry struct {
	Value    string    `json:"value"`
	StoredAt time.Time `json:"stored_at"`
}

// cacheKey derives a stable key for a (registry, package) pair.
func cacheKey(registryURL, packageID string) string {
	return registryURL + "\x00" + packageID
}

// resolveVersionFn is the actual registry lookup performed on a cache miss. It
// is a variable so tests can substitute a fake (avoiding a real network call).
var resolveVersionFn = ResolveVersion

// resolveVersionForFrameworkFn is the TFM-aware lookup used by renderers. It is
// a separate seam so the version-filtering logic can be tested offline.
var resolveVersionForFrameworkFn = ResolveVersionForFramework

// persistVersionCacheFn is the cache write used by both resolve paths. It is a
// seam for testing write failures without manipulating filesystem permissions.
var persistVersionCacheFn = persistVersionCache

// cacheMu serializes the read-modify-write cycle on a cache file. Two bridge
// layers resolving packages concurrently must not overwrite each other's
// entries or lose a freshly resolved version.
var cacheMu sync.Mutex

// versionStore loads (or initializes) the package-version cache file for a
// single bridge. Every bridge keeps its own file
// (~/.domaincraft/cache/<bridge>/packages.json), so with many bridges the
// caches never mix and a corrupt one only affects its own bridge.
func versionStore(bridgeID string) (map[string]versionEntry, error) {
	dir, err := appdir.CacheDir(bridgeID)
	if err != nil {
		return nil, err
	}
	var data struct {
		Entries map[string]versionEntry `json:"entries"`
	}
	// A missing or corrupt cache file yields an empty cache rather than an
	// error, so a broken cache never breaks the caller.
	if err := fsutil.ReadJSON(filepath.Join(dir, CacheFileName), &data); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if data.Entries == nil {
		data.Entries = make(map[string]versionEntry)
	}
	return data.Entries, nil
}

// persistVersionCache writes the cache file atomically and reports failures.
// A failed write is surfaced to the caller (the renderer logs it as a warning);
// silently discarding it made a broken cache path indistinguishable from a
// successful resolution.
func persistVersionCache(bridgeID string, entries map[string]versionEntry) error {
	dir, err := appdir.CacheDir(bridgeID)
	if err != nil {
		return fmt.Errorf("resolve package cache dir for %q: %w", bridgeID, err)
	}
	if err := fsutil.WriteJSON(filepath.Join(dir, CacheFileName), struct {
		Entries map[string]versionEntry `json:"entries"`
	}{Entries: entries}); err != nil {
		return fmt.Errorf("write package version cache for %q: %w", bridgeID, err)
	}
	return nil
}

// ResolveVersionCached resolves the latest stable version of a package from its
// registry, caching the result per bridge for DefaultCacheTTL. A fresh cached
// value avoids a network round-trip on every generation; a stale one triggers a
// refresh. A corrupt or missing cache is ignored and simply re-resolved.
func ResolveVersionCached(bridgeID, registryURL, packageID string) (string, error) {
	key := cacheKey(registryURL, packageID)

	cacheMu.Lock()
	defer cacheMu.Unlock()

	entries, err := versionStore(bridgeID)
	if err != nil {
		// Cache directory unusable — resolve uncached rather than fail.
		return resolveVersionFn(registryURL, packageID)
	}

	if e, ok := entries[key]; ok && e.Value != "" && time.Since(e.StoredAt) <= DefaultCacheTTL {
		return e.Value, nil
	}

	version, err := resolveVersionFn(registryURL, packageID)
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", nil
	}

	entries[key] = versionEntry{Value: version, StoredAt: time.Now()}
	if err := persistVersionCacheFn(bridgeID, entries); err != nil {
		return "", err
	}
	return version, nil
}

// ResolveVersionCachedForFramework is the TFM-aware counterpart of
// ResolveVersionCached. The framework is part of the cache key: the same bridge
// regenerated for net10.0 and net11.0 must not reuse an incompatible version.
func ResolveVersionCachedForFramework(bridgeID, registryURL, packageID, targetFramework string) (string, error) {
	key := cacheKey(registryURL, packageID) + "\x00" + targetFramework

	cacheMu.Lock()
	defer cacheMu.Unlock()

	entries, err := versionStore(bridgeID)
	if err != nil {
		return resolveVersionForFrameworkFn(registryURL, packageID, targetFramework)
	}
	if e, ok := entries[key]; ok && e.Value != "" && time.Since(e.StoredAt) <= DefaultCacheTTL {
		return e.Value, nil
	}

	version, err := resolveVersionForFrameworkFn(registryURL, packageID, targetFramework)
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", nil
	}
	entries[key] = versionEntry{Value: version, StoredAt: time.Now()}
	if err := persistVersionCacheFn(bridgeID, entries); err != nil {
		return "", err
	}
	return version, nil
}
