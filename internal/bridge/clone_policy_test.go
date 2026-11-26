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

// TestMaybeUpdatePolicies covers the decision gate that was previously
// untested: disabled checks, no policy, decline, prompt error and the
// pre-prompt contract gate.
func TestMaybeUpdatePolicies(t *testing.T) {
	tests := []struct {
		name        string
		interval    time.Duration
		remoteYAML  string
		pin         string
		confirm     func(RegistryEntry, *Update) (bool, error)
		wantWarn    string
		wantConfirm bool
		wantErr     bool
		wantNoHTTP  bool
	}{
		{
			name:       "disabled interval skips the remote",
			interval:   -1,
			remoteYAML: "name: test\ncontract_version: 1\n",
			wantNoHTTP: true,
		},
		{
			name:       "fresh cache skips the remote",
			interval:   24 * time.Hour,
			remoteYAML: "name: test\ncontract_version: 1\n",
			wantNoHTTP: true,
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
			remoteYAML: "name: test\ncontract_version: 3\n",
			wantWarn:   "skipping update",
		},
		{
			name:       "unreadable remote contract skips the prompt",
			remoteYAML: "contract_version: [1, 2\n",
			wantWarn:   "skipping update",
		},
		{
			name:       "pinned checkout skips the remote",
			remoteYAML: "name: test\ncontract_version: 1\n",
			pin:        "v1.2.3",
			wantNoHTTP: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !gitAvailable() {
				t.Skip("git not installed")
			}
			root := t.TempDir()
			appdir.SetBaseForTesting(root)
			t.Cleanup(func() { appdir.SetBaseForTesting("") })

			entry := cacheEntry("policy-bridge", "owner/policy-bridge")
			cacheDir := CachePath(entry)
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				t.Fatal(err)
			}
			// Update detection must read a real HEAD: a stale metadata commit
			// is deliberately ignored when git cannot be consulted.
			makeBridgeRepo(t, cacheDir, "main")
			head := testGit(t, cacheDir, "rev-parse", "HEAD")
			lastChecked := time.Now().Add(-48 * time.Hour)
			if tt.name == "fresh cache skips the remote" {
				lastChecked = time.Now()
			}
			if err := WriteMeta(entry, &CacheMeta{
				ID:            entry.ID,
				Source:        entry.GitHub,
				Version:       "1.0.0",
				Commit:        head,
				Branch:        "main",
				Pin:           tt.pin,
				LastCheckedAt: lastChecked,
			}); err != nil {
				t.Fatal(err)
			}

			stub := stubGitHubAPI(t, "remote-head", tt.remoteYAML)

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
				Warn:          func(format string, _ ...any) { warnings = append(warnings, format) },
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
			if tt.wantNoHTTP && stub.Calls() != 0 {
				t.Fatalf("remote was contacted %d times, want 0", stub.Calls())
			}
		})
	}
}

// TestCacheIsolationBetweenOwners covers the cache key collision: two GitHub
// repos with the same name but different owners must never share a checkout.
// A cache entry carrying another owner's metadata is replaced, not reused.
func TestCacheIsolationBetweenOwners(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	alice := RegistryEntry{ID: cacheIDForGitHub("alice/utils"), GitHub: "alice/utils"}
	bob := RegistryEntry{ID: cacheIDForGitHub("bob/utils"), GitHub: "bob/utils"}
	if CachePath(alice) == CachePath(bob) {
		t.Fatalf("both owners resolved to the same cache dir %q", CachePath(alice))
	}
	for _, e := range []RegistryEntry{alice, bob} {
		if err := validateCacheID(e.ID); err != nil {
			t.Fatalf("derived cache ID %q is unsafe: %v", e.ID, err)
		}
		dir := CachePath(e)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Simulate the old collision: both dirs hold alice's checkout.
		writeBridgeYAML(t, dir, "alice-1.0.0")
	}
	if err := WriteMeta(alice, &CacheMeta{ID: alice.ID, Source: alice.GitHub, Version: "alice-1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(bob, &CacheMeta{ID: bob.ID, Source: alice.GitHub, Version: "alice-1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if !IsCached(bob) {
		t.Fatal("bob's foreign checkout should look cached before the fix")
	}

	// Local bare remote stands in for github.com/bob/utils.git.
	remote, _ := newRemoteWithBridge(t, root, "main", "bob-2.0.0")

	home := t.TempDir()
	cfg := "[url \"file:///" + filepath.ToSlash(remote) + "\"]\n\tinsteadOf = https://github.com/bob/utils.git\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path, err := EnsureBridge(bob, nil)
	if err != nil {
		t.Fatalf("EnsureBridge(bob): %v", err)
	}
	if path != CachePath(bob) {
		t.Fatalf("EnsureBridge(bob) = %q, want %q", path, CachePath(bob))
	}
	data, err := os.ReadFile(filepath.Join(path, "bridge.yaml"))
	if err != nil || !strings.Contains(string(data), "bob-2.0.0") {
		t.Fatalf("bob served a foreign checkout: %q, %v", data, err)
	}
	meta, err := ReadMeta(bob)
	if err != nil || meta == nil || meta.Source != bob.GitHub {
		t.Fatalf("bob metadata = %+v, %v; want source %q", meta, err, bob.GitHub)
	}
	data, err = os.ReadFile(filepath.Join(CachePath(alice), "bridge.yaml"))
	if err != nil || !strings.Contains(string(data), "alice-1.0.0") {
		t.Fatalf("alice's cache was modified: %q, %v", data, err)
	}
}

// TestApplyUpdateRejectsUnsupportedContract checks the hard invariant at the
// apply site, not only the isolated CheckContractVersion predicate: a bridge
// declaring a newer contract must never be fetched into the cache.
func TestApplyUpdateRejectsUnsupportedContract(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}

	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	remote, src := newRemoteWithBridge(t, root, "main", "")

	entry := cacheEntry("contract-bridge", "owner/contract-bridge")
	cacheDir := CachePath(entry)
	testGit(t, root, "clone", "-q", remote, cacheDir)
	if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Publish a bridge.yaml the core cannot support.
	if err := os.WriteFile(filepath.Join(src, "bridge.yaml"), []byte("name: test\nversion: \"2.0.0\"\ncontract_version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, src, "add", ".")
	testGit(t, src, "commit", "-q", "-m", "bump contract")
	testGit(t, src, "push", "-q", "origin", "main")

	stubGitHubAPI(t, testGit(t, remote, "rev-parse", "HEAD"), "")
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

// TestApplyUpdateFallbackRejectsUnsupportedContract pins the gate on the clean
// re-clone path. A failed in-place fetch used to bypass the contract check
// entirely, so a bridge with a newer contract landed in the cache.
func TestApplyUpdateFallbackRejectsUnsupportedContract(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	remote, src := newRemoteWithBridge(t, root, "main", "")

	entry := cacheEntry("fallback-contract", "owner/fallback-contract")
	cacheDir := CachePath(entry)
	testGit(t, root, "clone", "-q", remote, cacheDir)
	if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
		t.Fatal(err)
	}

	// Publish a contract this core cannot support on the default branch.
	if err := os.WriteFile(filepath.Join(src, "bridge.yaml"), []byte("name: test\nversion: \"2.0.0\"\ncontract_version: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, src, "add", ".")
	testGit(t, src, "commit", "-q", "-m", "bump contract")
	testGit(t, src, "push", "-q", "origin", "main")

	// Rewrite the clone URL to the local remote so the fallback clone is offline.
	home := t.TempDir()
	cfg := "[url \"file:///" + filepath.ToSlash(remote) + "\"]\n\tinsteadOf = https://github.com/owner/fallback-contract.git\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// A syntactically valid but nonexistent branch forces the fallback path.
	u := &Update{RemoteBranch: "missing-branch"}
	if _, err := ApplyUpdate(entry, u); err == nil || !strings.Contains(err.Error(), "contract") {
		t.Fatalf("ApplyUpdate fallback error = %v, want a contract rejection", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "bridge.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported fallback clone was left in the cache: %v", err)
	}
}
