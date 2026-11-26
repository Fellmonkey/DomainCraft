package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

// S7: a user-supplied pin must be a git ref, not a git option or a revision
// expression. Validation happens before rev-parse/checkout run.
func TestValidatePinRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{name: "commit sha", ref: "9f8e7d6c5b4a"},
		{name: "tag", ref: "v1.2.3"},
		{name: "branch with slash", ref: "feature/branch"},
		{name: "plus suffix", ref: "release+build"},
		{name: "empty", ref: "", wantErr: true},
		{name: "leading dash", ref: "--upload-pack=sh", wantErr: true},
		{name: "dash branch", ref: "-dangerous", wantErr: true},
		{name: "dotdot traversal", ref: "release/../../main", wantErr: true},
		{name: "double slash", ref: "feature//branch", wantErr: true},
		{name: "revision expression", ref: "HEAD~1", wantErr: true},
		{name: "caret expression", ref: "HEAD^", wantErr: true},
		{name: "at brace expression", ref: "main@{1}", wantErr: true},
		{name: "newline", ref: "main\n--exec", wantErr: true},
		{name: "question mark", ref: "v1?", wantErr: true},
		{name: "backslash", ref: `a\b`, wantErr: true},
		{name: "leading slash", ref: "/main", wantErr: true},
		{name: "trailing slash", ref: "main/", wantErr: true},
		{name: "lock suffix", ref: "branch.lock", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePinRef(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePinRef(%q) error = %v, wantErr %v", tt.ref, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "invalid pinned ref") {
				t.Errorf("error = %q, want it to name the invalid ref", err)
			}
		})
	}

	dir := t.TempDir()
	if err := checkoutPinnedRef(dir, "--upload-pack=sh"); err == nil {
		t.Error("checkoutPinnedRef accepted an option-like ref without running validation")
	}
}

// S8: the pin is persisted in cache metadata and legacy metadata (no pin
// field) keeps decoding with an empty pin.
func TestRecordPinIsPersisted(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	entry := cacheEntry("pinned", "owner/pinned")
	legacy := &CacheMeta{ID: entry.ID, Source: entry.GitHub, Version: "1.0.0", Branch: "main"}
	if err := WriteMeta(entry, legacy); err != nil {
		t.Fatal(err)
	}
	meta, err := ReadMeta(entry)
	if err != nil || meta.Pin != "" {
		t.Fatalf("legacy metadata pin = %q, %v; want empty", meta.Pin, err)
	}

	if err := recordPin(entry, "v1.2.3"); err != nil {
		t.Fatalf("recordPin: %v", err)
	}
	meta, err = ReadMeta(entry)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Pin != "v1.2.3" || meta.Version != "1.0.0" || meta.Branch != "main" {
		t.Fatalf("metadata after pin = %+v; want pin v1.2.3 and preserved version/branch", meta)
	}
}

// S11: git children get a minimal environment. A GIT_CONFIG_* injection, an
// SSH command override or a proxy override must never reach them.
func TestGitScrubbedEnvAllowlist(t *testing.T) {
	dangerous := []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_SSH_COMMAND",
		"GIT_PROXY_COMMAND", "GIT_ASKPASS", "GIT_TRACE", "GIT_EXEC_PATH",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	}
	for _, name := range dangerous {
		t.Setenv(name, "attacker-controlled")
	}

	got := make(map[string]string)
	for _, kv := range gitScrubbedEnv() {
		name, value, _ := strings.Cut(kv, "=")
		got[strings.ToUpper(name)] = value
	}
	for _, name := range dangerous {
		if value, ok := got[name]; ok {
			t.Errorf("git environment leaked %s=%q", name, value)
		}
	}
	if _, ok := got["PATH"]; !ok {
		t.Error("git environment dropped PATH")
	}
	_, hasHome := got["HOME"]
	_, hasProfile := got["USERPROFILE"]
	if !hasHome && !hasProfile {
		t.Error("git environment dropped HOME/USERPROFILE")
	}
}

// S12: cache paths are attacker-controlled. A planted symlink must stop the
// embedded materialization before any file is written through it.
func TestTryMaterializeEmbeddedRefusesSymlinks(t *testing.T) {
	if err := os.Symlink("target", filepath.Join(t.TempDir(), "symlink-probe")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	entry := Default().ByID("csharp-core")
	if entry == nil {
		t.Fatal("csharp-core not in registry")
	}
	tests := []struct {
		name  string
		plant func(t *testing.T, dst, outside string)
	}{
		{
			name: "symlink at bridge cache dir",
			plant: func(t *testing.T, dst, outside string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, dst); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink at child directory",
			plant: func(t *testing.T, dst, outside string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dst, "templates")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			appdir.SetBaseForTesting(root)
			t.Cleanup(func() { appdir.SetBaseForTesting("") })

			outside := t.TempDir()
			dst := CachePath(*entry)
			tt.plant(t, dst, outside)

			if ok, err := TryMaterializeEmbedded(*entry, nil); err == nil || ok {
				t.Fatalf("TryMaterializeEmbedded = %v, %v; want error", ok, err)
			} else if !strings.Contains(err.Error(), "symlink") {
				t.Errorf("error = %q, want it to mention the symlink", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("materialization wrote %d entries through the symlink into %s", len(entries), outside)
			}
		})
	}

	// A target outside the cache directory is rejected too.
	if err := refuseSymlinks(t.TempDir(), filepath.Join(t.TempDir(), "elsewhere")); err == nil {
		t.Error("refuseSymlinks accepted a target outside the base directory")
	}
}

// S2/S5/S14: an unreadable fetched manifest is a hard error, an oversized
// probe body is never truncated into a valid-looking one, and URL errors do
// not leak credentials or query strings.
func TestApplyUpdateFailsWhenFetchedManifestIsUnreadable(t *testing.T) {
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

	entry := cacheEntry("no-manifest", "owner/no-manifest")
	cacheDir := CachePath(entry)
	testGit(t, root, "clone", "-q", remote, cacheDir)
	if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
		t.Fatal(err)
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

	// The new upstream commit removes bridge.yaml, so `git show` on the
	// fetched tree fails. The old gate skipped the check in that case.
	if err := os.Remove(filepath.Join(src, "bridge.yaml")); err != nil {
		t.Fatal(err)
	}
	testGit(t, src, "add", "-A")
	testGit(t, src, "commit", "-q", "-m", "remove manifest")
	testGit(t, src, "push", "-q", "origin", "main")

	update, err := CheckForUpdate(entry, 0)
	if err != nil || update == nil {
		t.Fatalf("CheckForUpdate = %+v, %v; want an update", update, err)
	}
	if _, err := ApplyUpdate(entry, update); err == nil {
		t.Fatal("ApplyUpdate accepted a fetched tree without bridge.yaml")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "bridge.yaml")); err != nil {
		t.Fatalf("cached bridge.yaml was lost: %v", err)
	}
}

func TestHTTPProbeLimitsAndRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = w.Write(make([]byte, maxProbeBodyBytes+1))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tests := []struct {
		name    string
		url     string
		wantErr string
		hidden  string
	}{
		{name: "oversized body fails", url: server.URL + "/large", wantErr: "exceeds"},
		{name: "url errors are redacted", url: strings.Replace(server.URL, "http://", "http://user:pass@", 1) + "/missing?token=secret", wantErr: "http 404 for http://", hidden: "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := httpGetBody(tt.url)
			if err == nil {
				t.Fatalf("httpGetBody(%q) succeeded, want error", tt.url)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
			if tt.hidden != "" && strings.Contains(err.Error(), tt.hidden) {
				t.Errorf("error = %q, leaked %q", err, tt.hidden)
			}
			if strings.Contains(err.Error(), "user:pass") {
				t.Errorf("error = %q, leaked userinfo", err)
			}
		})
	}
}

// F35: a redirect must be rejected before the follow-up request reaches the
// new host. Checking resp.Request.URL after client.Do is too late — the target
// has already been fetched.
func TestHTTPProbeRejectsRedirectBeforeFetch(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("secret-body"))
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/payload?token=super-secret", http.StatusFound)
	}))
	defer origin.Close()

	_, err := httpGetBody(origin.URL + "/start")
	if err == nil {
		t.Fatal("httpGetBody followed a redirect to a forbidden host")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %q, want a redirect rejection", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Errorf("error = %q, leaked the redirect target's query", err)
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("redirect target received %d requests, want none", n)
	}
}

// S9: probe requests are bound to the command context and the accessor does
// not mutate the package-level context.
func TestHTTPProbeUsesCommandContext(t *testing.T) {
	previous := CommandContext()
	defer SetCommandContext(previous)

	ctx, cancel := context.WithCancel(context.Background())
	SetCommandContext(ctx)
	if got := CommandContext(); got != ctx {
		t.Fatalf("CommandContext() = %v, want the wired context", got)
	}
	cancel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	if _, err := httpGetBody(server.URL); err == nil {
		t.Fatal("httpGetBody ignored the cancelled command context")
	}
}

// F31: the branch handed to `git fetch` may come from cache metadata, so it
// must pass the same strict git-ref grammar used for pinned refs.
func TestValidateFetchBranch(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		{name: "main", branch: "main"},
		{name: "feature branch", branch: "feature/branch"},
		{name: "release with plus", branch: "release+build"},
		{name: "tag-like name", branch: "v1.2.3"},
		{name: "empty", branch: "", wantErr: true},
		{name: "leading dash", branch: "--upload-pack=sh", wantErr: true},
		{name: "dash branch", branch: "-dangerous", wantErr: true},
		{name: "dotdot traversal", branch: "release/../../main", wantErr: true},
		{name: "double slash", branch: "feature//branch", wantErr: true},
		{name: "at brace expression", branch: "main@{1}", wantErr: true},
		{name: "leading slash", branch: "/main", wantErr: true},
		{name: "trailing slash", branch: "main/", wantErr: true},
		{name: "newline", branch: "main\n--exec", wantErr: true},
		{name: "lock suffix", branch: "branch.lock", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ApplyUpdate validates the fetch branch with validatePinRef and
			// wraps its error with this wording; check the same wrapper.
			err := validatePinRef(tt.branch)
			if err != nil {
				err = fmt.Errorf("invalid fetch branch %q: %w", tt.branch, err)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateFetchBranch(%q) = %v, wantErr %v", tt.branch, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "invalid fetch branch") {
				t.Errorf("error = %q, want it to name the invalid fetch branch", err)
			}
		})
	}
}

// F31 wiring: a hostile branch from either source (Update or cache metadata)
// is rejected before git runs. Without the guard ApplyUpdate falls through to
// a clone, so the validation error is the observable contract here.
func TestApplyUpdateRejectsUnsafeFetchBranch(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := cacheEntry("unsafe-branch", "owner/unsafe-branch")
	bad := "--upload-pack=sh"

	// Keep the no-guard baseline offline too: rewrite the clone URL to a
	// missing local path so an unguarded fall-through fails without network.
	home := t.TempDir()
	missing := filepath.ToSlash(filepath.Join(home, "missing.git"))
	cfg := "[url \"file:///" + missing + "\"]\n\tinsteadOf = https://github.com/owner/unsafe-branch.git\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Source 1: the Update passed by the caller.
	if _, err := ApplyUpdate(entry, &Update{RemoteBranch: bad}); err == nil ||
		!strings.Contains(err.Error(), "invalid fetch branch") {
		t.Errorf("ApplyUpdate with hostile Update branch = %v, want validation error", err)
	}

	// Source 2: the branch recorded in cache metadata.
	if err := WriteMeta(entry, &CacheMeta{ID: entry.ID, Source: entry.GitHub, Branch: bad}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyUpdate(entry, nil); err == nil ||
		!strings.Contains(err.Error(), "invalid fetch branch") {
		t.Errorf("ApplyUpdate with hostile metadata branch = %v, want validation error", err)
	}
}

// F32: when the in-place update fails and the fallback re-clone also fails,
// both causes and the cache path are reported, with the original failure first.
func TestApplyUpdateJoinsFastPathFailureWithCloneFailure(t *testing.T) {
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

	entry := cacheEntry("joined-errors", "owner/joined-errors")
	cacheDir := CachePath(entry)
	testGit(t, root, "clone", "-q", remote, cacheDir)

	// A syntactically valid but nonexistent branch makes the fetch fail, so the
	// update takes the re-clone fallback.
	if err := WriteMeta(entry, &CacheMeta{ID: entry.ID, Source: entry.GitHub, Version: "1.0.0", Branch: "missing-branch"}); err != nil {
		t.Fatal(err)
	}

	// Rewrite the clone URL to a missing local path: the fallback fails fast,
	// offline, and the original fetch cause must survive in the joined error.
	home := t.TempDir()
	missing := filepath.ToSlash(filepath.Join(home, "missing.git"))
	cfg := "[url \"file:///" + missing + "\"]\n\tinsteadOf = https://github.com/owner/joined-errors.git\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	_, err := ApplyUpdate(entry, nil)
	if err == nil {
		t.Fatal("ApplyUpdate succeeded despite a failed fetch and re-clone")
	}
	msg := err.Error()
	fetch := strings.Index(msg, `fetch branch "missing-branch"`)
	clone := strings.Index(msg, "re-clone bridge")
	if fetch < 0 {
		t.Errorf("error = %q, want the original fetch failure", msg)
	}
	if clone < 0 || !strings.Contains(msg, cacheDir) {
		t.Errorf("error = %q, want the re-clone failure with cache path %q", msg, cacheDir)
	}
	if fetch >= 0 && clone >= 0 && fetch > clone {
		t.Errorf("error = %q, want the original fetch failure before the cleanup failure", msg)
	}
}

// R3: when the remote probe and the freshness write both fail, both causes must
// reach the caller. Dropping the write error left the timestamp unpersisted, so
// every later run paid the probe timeout again with no diagnostic.
func TestCheckForUpdateJoinsCacheWriteFailure(t *testing.T) {
	// No t.Parallel: the cache base and httpGetBodyFn are package globals.
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	entry := cacheEntry("joined-write-failure", "owner/joined-write-failure")
	if err := WriteMeta(entry, &CacheMeta{
		ID: entry.ID, Source: entry.GitHub, Version: "1.0.0", Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}

	probeErr := errors.New("probe offline")
	originalGet := httpGetBodyFn
	t.Cleanup(func() { httpGetBodyFn = originalGet })
	httpGetBodyFn = func(string) ([]byte, error) {
		// Metadata has already been read; break the cache dir before WriteMeta
		// so the freshness write fails without a symlink or permission trick.
		dir := CachePath(entry)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("remove cache dir: %v", err)
		}
		if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("replace cache dir with a file: %v", err)
		}
		return nil, probeErr
	}

	_, err := CheckForUpdate(entry, 0)
	if err == nil {
		t.Fatal("CheckForUpdate hid both the probe and metadata-write failures")
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("error = %q, want the probe failure to stay inspectable", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "record bridge update check") {
		t.Errorf("error = %q, want the cache-write context", msg)
	}
	if !strings.Contains(msg, probeErr.Error()) {
		t.Errorf("error = %q, want the network failure context", msg)
	}
}

// --check-updates is a status query: probing the remote must not rewrite
// .domaincraft-meta.json. Generation-time probes still record the attempt.
func TestStatusCheckDoesNotMutateCacheMeta(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	// No t.Parallel: the cache base and httpGetBodyFn are package globals.
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	entry := cacheEntry("status-readonly", "owner/status-readonly")
	dir := CachePath(entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, dir, "main")
	head := testGit(t, dir, "rev-parse", "HEAD")
	checkedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	if err := WriteMeta(entry, &CacheMeta{
		ID: entry.ID, Source: entry.GitHub, Version: "1.0.0",
		Commit: head, Branch: "main", LastCheckedAt: checkedAt,
	}); err != nil {
		t.Fatal(err)
	}

	orig := httpGetBodyFn
	t.Cleanup(func() { httpGetBodyFn = orig })
	httpGetBodyFn = func(url string) ([]byte, error) {
		switch {
		case containsAny(url, "/commits/"):
			return []byte(`{"sha":"abcdef1234567890"}`), nil
		case containsAny(url, "/bridge.yaml"):
			return []byte("name: b\ncontract_version: 1\n"), nil
		case containsAny(url, "/repos/"):
			return []byte(`{"default_branch":"main"}`), nil
		}
		return nil, os.ErrNotExist
	}

	st := entry.Status(true)
	if !st.RemoteReachable || !st.Outdated {
		t.Fatalf("status = %+v, want a reachable, supported update", st)
	}

	after, err := ReadMeta(entry)
	if err != nil {
		t.Fatal(err)
	}
	if after == nil {
		t.Fatal("cache metadata disappeared after a status check")
	}
	if !after.LastCheckedAt.Equal(checkedAt) || after.Commit != head {
		t.Fatalf("status check mutated cache metadata: got %+v, want LastCheckedAt %v and Commit %s", after, checkedAt, head)
	}

	// The ordinary (generation-time) probe must keep its previous behavior.
	if _, err := CheckForUpdate(entry, 0); err != nil {
		t.Fatal(err)
	}
	written, err := ReadMeta(entry)
	if err != nil {
		t.Fatal(err)
	}
	if written.LastCheckedAt.Equal(checkedAt) {
		t.Fatal("CheckForUpdate stopped recording LastCheckedAt")
	}
}
