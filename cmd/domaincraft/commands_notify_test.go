package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

func TestCachedLatestCoreVersion(t *testing.T) {
	orig := latestReleaseForNoticeFn
	defer func() { latestReleaseForNoticeFn = orig }()

	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	latestReleaseForNoticeFn = func() (*githubRelease, error) {
		return decodeRelease([]byte(`{"tag_name":"v9.9.9","assets":[]}`))
	}

	latest, err := cachedLatestCoreVersion()
	if err != nil {
		t.Fatalf("cachedLatestCoreVersion: %v", err)
	}
	if latest != "9.9.9" {
		t.Fatalf("latest = %q, want 9.9.9", latest)
	}

	// Network is now down — the fresh cache must still serve the same version.
	latestReleaseForNoticeFn = func() (*githubRelease, error) {
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
	orig := latestReleaseForNoticeFn
	defer func() { latestReleaseForNoticeFn = orig }()

	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	calls := 0
	latestReleaseForNoticeFn = func() (*githubRelease, error) {
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

func TestNoticeClientHasShortTimeout(t *testing.T) {
	if noticeClient.Timeout > 2*time.Second {
		t.Errorf("notice timeout = %v, want at most 2s", noticeClient.Timeout)
	}
	if noticeClient.Timeout == httpClient.Timeout {
		t.Errorf("notice client shares the download timeout %v", httpClient.Timeout)
	}
}

func TestCachedLatestCoreVersionSurfacesCacheWriteFailure(t *testing.T) {
	orig := latestReleaseForNoticeFn
	defer func() { latestReleaseForNoticeFn = orig }()

	base := t.TempDir()
	appdir.SetBaseForTesting(base)
	t.Cleanup(func() { appdir.SetBaseForTesting("") })
	cacheDir, err := appdir.CacheDir("core")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cacheDir, "core-update.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	latestReleaseForNoticeFn = func() (*githubRelease, error) {
		return decodeRelease([]byte(`{"tag_name":"v9.9.9","assets":[]}`))
	}
	if _, err := cachedLatestCoreVersion(); err == nil {
		t.Fatal("cache write failure was silently discarded")
	}
}
