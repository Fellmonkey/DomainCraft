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
	httpGetBodyFn = func(url string) ([]byte, error) {
		return nil, fmt.Errorf("network down")
	}
	latest2, err := cachedLatestCoreVersion()
	if err != nil || latest2 != "9.9.9" {
		t.Errorf("cached latest = %q, %v; want 9.9.9", latest2, err)
	}
}
