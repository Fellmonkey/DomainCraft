package packages

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
)

const (
	testRegistryURL = "https://reg.example/{id}"
	testPackageID   = "demo.pkg"
	testFramework   = "net10.0"
)

// resetCacheRoot points the app base dir at a fresh temp dir for a test.
func resetCacheRoot(t *testing.T) {
	t.Helper()
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })
}

// seedVersion writes an entry directly into a bridge's cache file with the
// given stored-at time, bypassing the normal time.Now() used on resolution.
// The key matches the live ResolveVersionCachedForFramework path, so a seeded
// entry is visible to the resolver under test.
func seedVersion(t *testing.T, bridgeID, registryURL, packageID, framework, version string, storedAt time.Time) {
	t.Helper()
	key := frameworkCacheKey(registryURL, packageID, framework)
	dir := appdir.CacheDir(bridgeID)
	path := filepath.Join(dir, CacheFileName)
	if err := fsutil.WriteJSON(path, struct {
		Entries map[string]struct {
			Value    string    `json:"value"`
			StoredAt time.Time `json:"stored_at"`
		} `json:"entries"`
	}{
		Entries: map[string]struct {
			Value    string    `json:"value"`
			StoredAt time.Time `json:"stored_at"`
		}{
			key: {Value: version, StoredAt: storedAt},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// Different target frameworks must never share a cached version: a net10
// generation after a net11 one still needs a 10.x package.
func TestCachedVersionIsPerFramework(t *testing.T) {
	resetCacheRoot(t)
	old := resolveVersionForFrameworkFn
	defer func() { resolveVersionForFrameworkFn = old }()
	calls := 0
	resolveVersionForFrameworkFn = func(_, _, framework string) (string, error) {
		calls++
		if framework == "net10.0" {
			return "10.0.0", nil
		}
		return "11.0.0", nil
	}

	for _, framework := range []string{"net10.0", "net11.0", "net10.0"} {
		if _, err := ResolveVersionCachedForFramework("csharp-rest", testRegistryURL, "ef", framework); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("resolver calls = %d, want 2 (one miss per framework)", calls)
	}
}

func TestVersionStoreIsPerBridge(t *testing.T) {
	resetCacheRoot(t)

	dir := appdir.CacheDir("csharp-restful")
	want := filepath.Join(dir, CacheFileName)

	old := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) { return "1.0.0", nil }
	defer func() { resolveVersionForFrameworkFn = old }()

	if _, err := ResolveVersionCachedForFramework("csharp-restful", testRegistryURL, testPackageID, testFramework); err != nil {
		t.Fatalf("ResolveVersionCachedForFramework: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected per-bridge cache file at %s: %v", want, err)
	}
}

// TestResolveVersionCachedForFrameworkFreshHit verifies that a fresh cache
// entry short-circuits the registry lookup (no network, no resolver call).
func TestResolveVersionCachedForFrameworkFreshHit(t *testing.T) {
	resetCacheRoot(t)

	seedVersion(t, "demo", testRegistryURL, testPackageID, testFramework, "1.0.0", time.Now())

	// Guard: if the cache path is not taken, the fake below would be called.
	called := false
	old := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
		called = true
		return "9.9.9", nil
	}
	defer func() { resolveVersionForFrameworkFn = old }()

	got, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
	if err != nil {
		t.Fatalf("ResolveVersionCachedForFramework: %v", err)
	}
	if got != "1.0.0" {
		t.Fatalf("expected cached version 1.0.0, got %q", got)
	}
	if called {
		t.Fatal("registry must not be contacted for a fresh cache entry")
	}
}

// TestResolveVersionCachedForFrameworkStaleRefreshes verifies that an expired
// entry is re-resolved and the new version replaces it in the cache.
func TestResolveVersionCachedForFrameworkStaleRefreshes(t *testing.T) {
	resetCacheRoot(t)

	seedVersion(t, "demo", testRegistryURL, testPackageID, testFramework, "1.0.0", time.Now().Add(-48*time.Hour))

	oldFn := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
		return "2.0.0", nil
	}
	defer func() { resolveVersionForFrameworkFn = oldFn }()

	got, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
	if err != nil {
		t.Fatalf("ResolveVersionCachedForFramework: %v", err)
	}
	if got != "2.0.0" {
		t.Fatalf("expected refreshed version 2.0.0, got %q", got)
	}

	// The refreshed value must be persisted and now serve as a fresh hit.
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
		t.Fatal("must not contact registry after refresh")
		return "", nil
	}
	got2, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
	if err != nil || got2 != "2.0.0" {
		t.Fatalf("expected persisted refresh to serve 2.0.0, got %q (err %v)", got2, err)
	}
}

// TestResolveVersionCachedForFrameworkTTLBoundary pins the 24h cutoff: just
// inside the TTL is a hit, just outside is a miss.
func TestResolveVersionCachedForFrameworkTTLBoundary(t *testing.T) {
	tests := []struct {
		name     string
		storedAt time.Time
		wantHit  bool
	}{
		{name: "just inside TTL", storedAt: time.Now().Add(-DefaultCacheTTL + time.Second), wantHit: true},
		{name: "just outside TTL", storedAt: time.Now().Add(-DefaultCacheTTL - time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCacheRoot(t)
			seedVersion(t, "demo", testRegistryURL, testPackageID, testFramework, "1.0.0", tt.storedAt)

			called := false
			old := resolveVersionForFrameworkFn
			resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
				called = true
				return "2.0.0", nil
			}
			defer func() { resolveVersionForFrameworkFn = old }()

			got, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
			if err != nil {
				t.Fatal(err)
			}
			if called == tt.wantHit {
				t.Fatalf("registry called = %v, wantHit %v", called, tt.wantHit)
			}
			want := "2.0.0"
			if tt.wantHit {
				want = "1.0.0"
			}
			if got != want {
				t.Fatalf("version = %q, want %q", got, want)
			}
		})
	}
}

// TestResolveVersionCachedForFrameworkRegistryFailures covers the two paths
// where no version may be cached: a registry error and an empty result.
func TestResolveVersionCachedForFrameworkRegistryFailures(t *testing.T) {
	tests := []struct {
		name    string
		version string
		err     error
	}{
		{name: "registry error", err: os.ErrNotExist},
		{name: "empty version", version: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCacheRoot(t)
			old := resolveVersionForFrameworkFn
			resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) { return tt.version, tt.err }
			defer func() { resolveVersionForFrameworkFn = old }()

			got, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
			if (err != nil) != (tt.err != nil) || got != tt.version {
				t.Fatalf("got %q, %v; want %q, %v", got, err, tt.version, tt.err)
			}

			// No entry may be cached from a failed or empty resolution.
			called := false
			resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
				called = true
				return "3.0.0", nil
			}
			got, err = ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
			if err != nil || !called || got != "3.0.0" {
				t.Fatalf("second call = %q, %v, called=%v; want 3.0.0 without error", got, err, called)
			}
		})
	}
}

// TestResolveVersionCachedForFrameworkIsolation verifies caches are per bridge:
// a version cached for one bridge is not served to another.
func TestResolveVersionCachedForFrameworkIsolation(t *testing.T) {
	resetCacheRoot(t)

	seedVersion(t, "bridge-a", testRegistryURL, testPackageID, testFramework, "1.0.0", time.Now())

	oldFn := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
		return "9.9.9", nil
	}
	defer func() { resolveVersionForFrameworkFn = oldFn }()

	// bridge-b has no cache -> must resolve (fresh) via the registry fake.
	got, err := ResolveVersionCachedForFramework("bridge-b", testRegistryURL, testPackageID, testFramework)
	if err != nil {
		t.Fatalf("ResolveVersionCachedForFramework(bridge-b): %v", err)
	}
	if got != "9.9.9" {
		t.Fatalf("bridge-b must not read bridge-a's cache, got %q", got)
	}
}

// A failed cache write must not discard a version that was successfully
// resolved from the registry.
func TestPersistFailureKeepsResolvedVersion(t *testing.T) {
	resetCacheRoot(t)

	persistErr := errors.New("disk full")
	oldPersist := persistVersionCacheFn
	persistVersionCacheFn = func(_ string, _ map[string]versionEntry) error { return persistErr }
	defer func() { persistVersionCacheFn = oldPersist }()

	oldResolve := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) { return "10.0.0", nil }
	defer func() { resolveVersionForFrameworkFn = oldResolve }()

	got, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
	if got != "10.0.0" {
		t.Fatalf("version = %q, want 10.0.0 (persist failure must not drop it)", got)
	}
	if !errors.Is(err, persistErr) || !strings.Contains(err.Error(), "persist package version cache") {
		t.Fatalf("err = %v, want a non-fatal persist failure", err)
	}
}

// TestLoadCacheIgnoresCorruptFile ensures a corrupt cache file degrades
// gracefully (fresh empty cache) instead of failing generation.
func TestLoadCacheIgnoresCorruptFile(t *testing.T) {
	resetCacheRoot(t)

	dir := appdir.CacheDir("demo")
	path := filepath.Join(dir, CacheFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A corrupt file must not fail resolution; it should fall through to a
	// fresh registry lookup.
	oldFn := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) { return "1.0.0", nil }
	defer func() { resolveVersionForFrameworkFn = oldFn }()

	got, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework)
	if err != nil {
		t.Fatalf("ResolveVersionCachedForFramework: %v", err)
	}
	if got != "1.0.0" {
		t.Fatalf("expected fresh resolve for corrupt cache, got %q", got)
	}
}

// TestResolveVersionCachedForFrameworkRepairsCorruptCache pins the recovery
// path: after a corrupt packages.json is discarded, the fresh value must be
// persisted so the next run resolves from cache instead of contacting the
// registry again.
func TestResolveVersionCachedForFrameworkRepairsCorruptCache(t *testing.T) {
	resetCacheRoot(t)

	dir := appdir.CacheDir("demo")
	path := filepath.Join(dir, CacheFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	oldFn := resolveVersionForFrameworkFn
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) {
		calls++
		return "1.0.0", nil
	}
	defer func() { resolveVersionForFrameworkFn = oldFn }()

	if _, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework); err != nil {
		t.Fatalf("first ResolveVersionCachedForFramework: %v", err)
	}
	if _, err := ResolveVersionCachedForFramework("demo", testRegistryURL, testPackageID, testFramework); err != nil {
		t.Fatalf("second ResolveVersionCachedForFramework: %v", err)
	}
	if calls != 1 {
		t.Fatalf("registry consulted %d times, want 1: corrupt cache was not rewritten", calls)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Entries map[string]versionEntry `json:"entries"`
	}
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatalf("cache file is still corrupt: %v", err)
	}
	key := frameworkCacheKey(testRegistryURL, testPackageID, testFramework)
	if got := stored.Entries[key].Value; got != "1.0.0" {
		t.Fatalf("cached version = %q, want 1.0.0", got)
	}
}

func TestResolveVersionCachedDoesNotHoldLockDuringResolve(t *testing.T) {
	resetCacheRoot(t)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = resolveVersionCached("slow", "key-a", func() (string, error) {
			close(started)
			<-release
			return "1.0.0", nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first resolver did not start")
	}

	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		_, _ = resolveVersionCached("slow", "key-b", func() (string, error) { return "2.0.0", nil })
	}()
	select {
	case <-fastDone:
	case <-time.After(2 * time.Second):
		t.Error("second resolution was blocked by the first registry lookup")
	}
	close(release)
	<-done
}
