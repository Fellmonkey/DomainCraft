package main

import (
	"fmt"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

func TestCachedLatestCoreVersion(t *testing.T) {
	orig := httpGetBodyFn
	defer func() { httpGetBodyFn = orig }()

	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	httpGetBodyFn = func(url string) ([]byte, error) {
		if url != releasesLatestURL {
			return nil, fmt.Errorf("unexpected url %s", url)
		}
		return []byte(`{"tag_name":"v9.9.9","assets":[]}`), nil
	}

	latest, err := cachedLatestCoreVersion()
	if err != nil {
		t.Fatalf("cachedLatestCoreVersion: %v", err)
	}
	if latest != "9.9.9" {
		t.Fatalf("latest = %q, want 9.9.9", latest)
	}

	// Network is now down — the fresh cache must still serve the same version.
	httpGetBodyFn = func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("network down")
	}
	latest2, err := cachedLatestCoreVersion()
	if err != nil || latest2 != "9.9.9" {
		t.Errorf("cached latest = %q, %v; want 9.9.9", latest2, err)
	}
}

// Regression: an offline check used to leave no cache entry, so every later
// command waited for the full HTTP timeout again.
func TestCachedLatestCoreVersionCachesFailedCheck(t *testing.T) {
	orig := httpGetBodyFn
	defer func() { httpGetBodyFn = orig }()

	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	calls := 0
	httpGetBodyFn = func(string) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("network down")
	}

	if _, err := cachedLatestCoreVersion(); err == nil {
		t.Fatal("expected the first check to return an error")
	}
	// The failed attempt is cached as an empty result: no notice and no second
	// network wait.
	latest, err := cachedLatestCoreVersion()
	if err != nil || latest != "" {
		t.Fatalf("second check = %q, %v; want empty result without error", latest, err)
	}
	if calls != 1 {
		t.Fatalf("network calls = %d, want 1 (failed attempt must be cached)", calls)
	}
}
