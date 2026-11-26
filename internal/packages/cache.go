package packages

import (
	"encoding/json"
	"errors"
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

// resolveVersionForFrameworkFn is the TFM-aware lookup used by renderers. It is
// a seam so the version-filtering logic can be tested offline.
var resolveVersionForFrameworkFn = ResolveVersionForFramework

// persistVersionCacheFn is the cache write used by both resolve paths. It is a
// seam for testing write failures without manipulating filesystem permissions.
var persistVersionCacheFn = persistVersionCache

// cacheMu serializes the read-modify-write cycle on a cache file. Two bridge
// layers resolving packages concurrently must not overwrite each other's
// entries or lose a freshly resolved version. It is a single global mutex, so
// resolution across all bridges is serialized; correctness does not depend on
// that serialization, and per-bridge locks are a possible future optimization.
var cacheMu sync.Mutex

// versionStore loads (or initializes) the package-version cache file for a
// single bridge. Every bridge keeps its own file
// (~/.domaincraft/cache/<bridge>/packages.json), so with many bridges the
// caches never mix and a corrupt one only affects its own bridge.
func versionStore(bridgeID string) (map[string]versionEntry, error) {
	dir := appdir.CacheDir(bridgeID)
	var data struct {
		Entries map[string]versionEntry `json:"entries"`
	}
	// A missing or corrupt cache file yields an empty cache rather than an
	// error; the normal resolve -> persist path then overwrites the unusable
	// bytes. Genuine storage failures (permissions, I/O) must not be masked as
	// a cache miss — that would overwrite the file and hide the real fault.
	if err := fsutil.ReadJSON(filepath.Join(dir, CacheFileName), &data); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if !errors.Is(err, os.ErrNotExist) && !errors.As(err, &syntaxErr) && !errors.As(err, &typeErr) {
			return nil, fmt.Errorf("read package version cache for %q: %w", bridgeID, err)
		}
		data.Entries = make(map[string]versionEntry)
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
	dir := appdir.CacheDir(bridgeID)
	if err := fsutil.WriteJSON(filepath.Join(dir, CacheFileName), struct {
		Entries map[string]versionEntry `json:"entries"`
	}{Entries: entries}); err != nil {
		return fmt.Errorf("write package version cache for %q: %w", bridgeID, err)
	}
	return nil
}

// resolveVersionCached is the single cache pipeline shared by both exported
// resolvers. key identifies the (registry, package[, framework]) entry; resolve
// performs the registry lookup on a miss. Both callers keep their own resolver
// seam, but the lock/store/TTL/resolve/persist sequence lives here once.
func resolveVersionCached(bridgeID, key string, resolve func() (string, error)) (string, error) {
	cacheMu.Lock()
	entries, err := versionStore(bridgeID)
	if err != nil {
		cacheMu.Unlock()
		// Cache directory unusable — resolve uncached rather than fail.
		return resolve()
	}
	if e, ok := entries[key]; ok && e.Value != "" && time.Since(e.StoredAt) <= DefaultCacheTTL {
		cacheMu.Unlock()
		return e.Value, nil
	}
	cacheMu.Unlock()

	// A registry lookup can take seconds. Do not hold the cache lock across it:
	// other layers must keep resolving while one upstream is slow.
	version, err := resolve()
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", nil
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	// Another goroutine may have resolved and stored this key meanwhile.
	entries, err = versionStore(bridgeID)
	if err != nil {
		return version, fmt.Errorf("reread package version cache for %q: %w", bridgeID, err)
	}
	if e, ok := entries[key]; ok && e.Value != "" && time.Since(e.StoredAt) <= DefaultCacheTTL {
		return e.Value, nil
	}
	entries[key] = versionEntry{Value: version, StoredAt: time.Now()}
	if err := persistVersionCacheFn(bridgeID, entries); err != nil {
		// The version is real: a broken cache must not turn a successful
		// registry lookup into a package-less generated project. Report the
		// write failure non-fatally and let the caller warn once.
		return version, fmt.Errorf("persist package version cache for %q: %w", bridgeID, err)
	}
	return version, nil
}

// frameworkCacheKey derives the cache key for a TFM-aware resolution. The
// framework is part of the key: the same bridge regenerated for net10.0 and
// net11.0 must never reuse an incompatible version.
func frameworkCacheKey(registryURL, packageID, targetFramework string) string {
	return cacheKey(registryURL, packageID) + "\x00" + targetFramework
}

// ResolveVersionCachedForFramework resolves the latest stable version of a
// package compatible with targetFramework, caching the result per bridge for
// DefaultCacheTTL. A fresh cached value avoids a network round-trip on every
// generation; a stale one triggers a refresh. A corrupt or missing cache is
// ignored and simply re-resolved.
func ResolveVersionCachedForFramework(bridgeID, registryURL, packageID, targetFramework string) (string, error) {
	key := frameworkCacheKey(registryURL, packageID, targetFramework)
	return resolveVersionCached(bridgeID, key, func() (string, error) {
		return resolveVersionForFrameworkFn(registryURL, packageID, targetFramework)
	})
}
