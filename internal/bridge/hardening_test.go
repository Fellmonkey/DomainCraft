package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
