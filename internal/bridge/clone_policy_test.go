package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// fakeUpdateHTTP reports a remote head that differs from the cached commit and,
// optionally, a bridge.yaml with an unsupported contract version.
func fakeUpdateHTTP(t *testing.T, remoteYAML string) {
	t.Helper()
	original := httpGetBodyFn
	t.Cleanup(func() { httpGetBodyFn = original })
	httpGetBodyFn = func(url string) ([]byte, error) {
		switch {
		case strings.Contains(url, "/commits/main"):
			return []byte(`{"sha":"remote-head"}`), nil
		case strings.Contains(url, "/repos/"):
			return []byte(`{"default_branch":"main"}`), nil
		case strings.Contains(url, "bridge.yaml"):
			return []byte(remoteYAML), nil
		}
		return nil, os.ErrNotExist
	}
}

// TestMaybeUpdatePolicies covers the decision gate that was previously
// untested: disabled checks, no policy, decline, prompt error and the
// pre-prompt contract gate.
func TestMaybeUpdatePolicies(t *testing.T) {
	tests := []struct {
		name        string
		interval    time.Duration
		remoteYAML  string
		confirm     func(RegistryEntry, *Update) (bool, error)
		wantWarn    string
		wantConfirm bool
		wantErr     bool
	}{
		{
			name:       "disabled interval skips the remote",
			interval:   -1,
			remoteYAML: "name: test\ncontract_version: 1\n",
		},
		{
			name:       "fresh cache skips the remote",
			interval:   24 * time.Hour,
			remoteYAML: "name: test\ncontract_version: 1\n",
		},
		{
			name:       "no policy warns without applying",
			remoteYAML: "name: test\ncontract_version: 1\n",
			wantWarn:   "has an update available",
		},
		{
			name:        "declined update keeps the cache",
			remoteYAML:  "name: test\ncontract_version: 1\n",
			confirm:     func(RegistryEntry, *Update) (bool, error) { return false, nil },
			wantConfirm: true,
		},
		{
			name:        "prompt error is propagated",
			remoteYAML:  "name: test\ncontract_version: 1\n",
			confirm:     func(RegistryEntry, *Update) (bool, error) { return false, errors.New("prompt failed") },
			wantConfirm: true,
			wantErr:     true,
		},
		{
			name:       "unsupported contract skips the prompt",
			remoteYAML: "name: test\ncontract_version: 2\n",
			wantWarn:   "skipping update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			appdir.SetBaseForTesting(root)
			t.Cleanup(func() { appdir.SetBaseForTesting("") })

			entry := cacheEntry("policy-bridge", "owner/policy-bridge")
			cacheDir := CachePath(entry)
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cacheDir, "bridge.yaml"), []byte("name: test\nversion: \"1.0.0\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			lastChecked := time.Now().Add(-48 * time.Hour)
			if tt.name == "fresh cache skips the remote" {
				lastChecked = time.Now()
			}
			if err := WriteMeta(entry, &CacheMeta{
				ID:            entry.ID,
				Source:        entry.GitHub,
				Version:       "1.0.0",
				Commit:        "cached-head",
				Branch:        "main",
				LastCheckedAt: lastChecked,
			}); err != nil {
				t.Fatal(err)
			}

			fakeUpdateHTTP(t, tt.remoteYAML)

			confirmed := false
			confirm := tt.confirm
			if confirm != nil {
				confirm = func(e RegistryEntry, u *Update) (bool, error) {
					confirmed = true
					return tt.confirm(e, u)
				}
			}

			var warnings []string
			opts := &EnsureOptions{
				Interval:      tt.interval,
				ConfirmUpdate: confirm,
				Warn:          func(format string, args ...any) { warnings = append(warnings, format) },
			}

			err := maybeUpdate(entry, opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("maybeUpdate error = %v, wantErr %v", err, tt.wantErr)
			}
			if confirmed != tt.wantConfirm {
				t.Fatalf("confirm called = %v, want %v", confirmed, tt.wantConfirm)
			}
			if tt.wantWarn == "" && len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			if tt.wantWarn != "" && !strings.Contains(strings.Join(warnings, " "), tt.wantWarn) {
				t.Fatalf("warnings %v do not contain %q", warnings, tt.wantWarn)
			}
		})
	}
}

// TestApplyUpdateRejectsUnsupportedContract checks the hard invariant at the
// apply site, not only the isolated shouldApplyContract predicate: a bridge
// declaring a newer contract must never be fetched into the cache.
func TestApplyUpdateRejectsUnsupportedContract(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}

	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	remote := filepath.Join(root, "remote.git")
	testGit(t, root, "init", "--bare", "-b", "main", "remote.git")
	testGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, src, "main")
	testGit(t, src, "remote", "add", "origin", remote)
	testGit(t, src, "push", "-q", "-u", "origin", "main")

	entry := cacheEntry("contract-bridge", "owner/contract-bridge")
	cacheDir := CachePath(entry)
	testGit(t, root, "clone", "-q", remote, cacheDir)
	if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	original := httpGetBodyFn
	defer func() { httpGetBodyFn = original }()
	httpGetBodyFn = func(url string) ([]byte, error) {
		switch {
		case strings.Contains(url, "/commits/main"):
			return []byte(`{"sha":"` + testGit(t, remote, "rev-parse", "HEAD") + `"}`), nil
		case strings.Contains(url, "/repos/"):
			return []byte(`{"default_branch":"main"}`), nil
		}
		return nil, os.ErrNotExist
	}

	// Publish a bridge.yaml the core cannot support.
	if err := os.WriteFile(filepath.Join(src, "bridge.yaml"), []byte("name: test\nversion: \"2.0.0\"\ncontract_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, src, "add", ".")
	testGit(t, src, "commit", "-q", "-m", "bump contract")
	testGit(t, src, "push", "-q", "origin", "main")

	update, err := CheckForUpdate(entry, 0)
	if err != nil || update == nil {
		t.Fatalf("CheckForUpdate = %+v, %v; want an update", update, err)
	}
	if _, err := ApplyUpdate(entry, update); err == nil {
		t.Fatal("ApplyUpdate accepted an unsupported contract version")
	}

	data, err := os.ReadFile(filepath.Join(cacheDir, "bridge.yaml"))
	if err != nil || !strings.Contains(string(data), `version: "1.0.0"`) {
		t.Fatalf("cached bridge.yaml changed: %q, %v", data, err)
	}
}
