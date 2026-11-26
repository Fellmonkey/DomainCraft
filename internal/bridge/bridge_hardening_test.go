package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

func TestParseGitHubRefAllowsSlashInPin(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		input    string
		repo     string
		ref      string
		wantRepo bool
		wantErr  bool
	}{
		{name: "branch with slash", input: "owner/repo@feature/branch", repo: "owner/repo", ref: "feature/branch", wantRepo: true},
		{name: "plain shorthand", input: "owner/repo", repo: "owner/repo", wantRepo: true},
		{name: "repo with extra segment", input: "owner/repo/extra", wantErr: true},
		{name: "option-like pin", input: "owner/repo@--upload-pack=sh", wantErr: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			repo, ref, ok, err := parseGitHubRef(c.input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseGitHubRef(%q) unexpectedly succeeded", c.input)
				}
				return
			}
			if err != nil || !ok || repo != c.repo || ref != c.ref {
				t.Fatalf("parseGitHubRef(%q) = (%q, %q, %t, %v)", c.input, repo, ref, ok, err)
			}
			if !c.wantRepo {
				t.Fatalf("parseGitHubRef(%q) unexpectedly matched a GitHub ref", c.input)
			}
		})
	}
}

func TestGithubOriginMatchesExactRepository(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "https origin", origin: "https://github.com/owner/repo.git", want: true},
		{name: "ssh origin", origin: "git@github.com:owner/repo.git", want: true},
		{name: "lookalike repository", origin: "https://github.com/owner/repo-evil.git", want: false},
		{name: "empty origin", origin: "", want: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := githubOriginMatches(c.origin, "owner/repo"); got != c.want {
				t.Errorf("githubOriginMatches(%q) = %t, want %t", c.origin, got, c.want)
			}
		})
	}
}

func TestCacheSourceMatchesFailsClosedWithoutProof(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")
	entry := cacheEntry("foreign-cache", "owner/repo")
	if err := os.MkdirAll(CachePath(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if cacheSourceMatches(entry) {
		t.Fatal("a cache without metadata or a matching remote was accepted as owned")
	}
}

// TestCacheSourceMatchesRepairsCorruptMeta pins F21: a truncated metadata file
// must not cost a working checkout. If git's origin proves ownership, the cache
// is kept and the metadata rebuilt. A foreign origin still fails closed.
func TestCacheSourceMatchesRepairsCorruptMeta(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := cacheEntry("corrupt-meta", "owner/corrupt-meta")
	dir := CachePath(entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, dir, "main")
	origin := "https://github.com/owner/corrupt-meta.git"
	if out := testGit(t, dir, "remote", "add", "origin", origin); out != "" {
		t.Fatalf("unexpected git output: %q", out)
	}
	// Simulate a truncated/partially written metadata file.
	if err := os.WriteFile(filepath.Join(dir, MetaFileName), []byte("{\"id\":\"corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeta(entry); err == nil {
		t.Fatal("test fixture: corrupt metadata should not decode")
	}
	if !cacheSourceMatches(entry) {
		t.Fatal("a matching git origin should prove ownership of a corrupt-meta cache")
	}
	meta, err := ReadMeta(entry)
	if err != nil || meta == nil {
		t.Fatalf("metadata was not rebuilt: %+v, %v", meta, err)
	}
	if meta.Source != entry.GitHub {
		t.Fatalf("rebuilt source = %q, want %q", meta.Source, entry.GitHub)
	}
	if meta.Version != "1.0.0" || meta.Commit == "" || meta.Branch != "main" {
		t.Fatalf("metadata not rebuilt from checkout: %+v", meta)
	}

	// A foreign origin must stay fail-closed and must not be repaired.
	testGit(t, dir, "remote", "set-url", "origin", "https://github.com/other/foreign.git")
	if err := os.WriteFile(filepath.Join(dir, MetaFileName), []byte(`{"id":"corrupt`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cacheSourceMatches(entry) {
		t.Fatal("a foreign origin was accepted as proof of ownership")
	}
	if _, err := ReadMeta(entry); err == nil {
		t.Fatal("foreign metadata was rewritten despite a mismatched origin")
	}
}

// TestGitHeadResultDistinguishesFailure pins F22's seam: a missing or damaged
// checkout is an error, never an empty but successfully read HEAD.
func TestGitHeadResultDistinguishesFailure(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := cacheEntry("head-result", "owner/head-result")
	dir := CachePath(entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if head, err := gitHeadResult(entry); err == nil {
		t.Fatalf("gitHeadResult on a non-repo = (%q, nil), want an error", head)
	}
	makeBridgeRepo(t, dir, "main")
	head, err := gitHeadResult(entry)
	if err != nil || head == "" {
		t.Fatalf("gitHeadResult on a valid checkout = (%q, %v)", head, err)
	}
	if want := testGit(t, dir, "rev-parse", "HEAD"); head != want {
		t.Fatalf("gitHeadResult = %q, want %q", head, want)
	}
}

// TestCheckForUpdateRejectsStaleCommitWhenGitFails pins F22: with no readable
// HEAD, a stale metadata commit must not be compared against the remote tip.
func TestCheckForUpdateRejectsStaleCommitWhenGitFails(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := cacheEntry("stale-commit", "owner/stale-commit")
	if err := os.MkdirAll(CachePath(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBridgeYAML(t, CachePath(entry), "1.0.0")
	if err := WriteMeta(entry, &CacheMeta{
		ID: entry.ID, Source: entry.GitHub, Version: "1.0.0", Commit: "stale-commit",
		Branch: "main", LastCheckedAt: time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	orig := httpGetBodyFn
	defer func() { httpGetBodyFn = orig }()
	httpGetBodyFn = func(url string) ([]byte, error) {
		switch {
		case strings.Contains(url, "/commits/main"):
			return []byte(`{"sha":"new-remote-commit"}`), nil
		case strings.Contains(url, "/repos/"):
			return []byte(`{"default_branch":"main"}`), nil
		}
		return nil, os.ErrNotExist
	}

	upd, err := CheckForUpdate(entry, 0)
	if err != nil || upd != nil {
		t.Fatalf("CheckForUpdate = (%+v, %v), want no update when HEAD is unreadable", upd, err)
	}
	meta, err := ReadMeta(entry)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Commit != "stale-commit" {
		t.Fatalf("stale commit was overwritten: %q", meta.Commit)
	}
	if meta.LastCheckedAt.Before(time.Now().Add(-time.Hour)) {
		t.Fatal("probe attempt was not recorded")
	}
}

// TestCheckForUpdateSyncsHeadBeforeComparing pins the other half of F22: a
// valid checkout is authoritative even when metadata carries an older commit.
func TestCheckForUpdateSyncsHeadBeforeComparing(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")

	entry := cacheEntry("head-sync", "owner/head-sync")
	dir := CachePath(entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, dir, "main")
	head := testGit(t, dir, "rev-parse", "HEAD")
	if err := WriteMeta(entry, &CacheMeta{
		ID: entry.ID, Source: entry.GitHub, Version: "1.0.0", Commit: "stale-commit",
		Branch: "main", LastCheckedAt: time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	orig := httpGetBodyFn
	defer func() { httpGetBodyFn = orig }()
	httpGetBodyFn = func(url string) ([]byte, error) {
		switch {
		case strings.Contains(url, "/commits/main"):
			return []byte(`{"sha":"` + head + `"}`), nil
		case strings.Contains(url, "/repos/"):
			return []byte(`{"default_branch":"main"}`), nil
		}
		return nil, os.ErrNotExist
	}

	upd, err := CheckForUpdate(entry, 0)
	if err != nil || upd != nil {
		t.Fatalf("CheckForUpdate = (%+v, %v), want no update after syncing HEAD", upd, err)
	}
	meta, err := ReadMeta(entry)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Commit != head {
		t.Fatalf("metadata commit = %q, want %q", meta.Commit, head)
	}
}

func TestFinishUpdateChecksPinnedContract(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")
	entry := cacheEntry("pin-contract", "owner/pin-contract")
	dir := CachePath(entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, dir, "main")
	body := "name: test-bridge\nversion: \"1.0.0\"\ncontract_version: 99\n"
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-q", "-m", "bump contract")
	pin := testGit(t, dir, "rev-parse", "HEAD")

	if _, err := finishUpdate(entry, "main", pin); err == nil || !strings.Contains(err.Error(), "contract") {
		t.Fatalf("finishUpdate accepted an unsupported pinned contract: %v", err)
	}
	// Fail closed: an unsupported pinned tree must not stay in the cache with a
	// pin that would make the next run skip its checks.
	if _, err := os.Stat(filepath.Join(dir, "bridge.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported pinned cache was left behind: %v", err)
	}
	if meta, err := ReadMeta(entry); err != nil || (meta != nil && meta.Pin != "") {
		t.Fatalf("unsupported pin was kept in metadata: %+v, %v", meta, err)
	}
}

func TestRunGitHonoursCommandContext(t *testing.T) {
	previous := CommandContext()
	defer SetCommandContext(previous)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	SetCommandContext(ctx)
	if _, err := runGit(t.TempDir(), "status"); err == nil {
		t.Fatal("runGit ignored a cancelled command context")
	}
}

// TestCloneGitContextHasDeadline pins the context source shared by the initial
// clone and every update command. CloneBridge used to pass the raw package
// context to exec.CommandContext, so a stalled remote hung forever.
func TestCloneGitContextHasDeadline(t *testing.T) {
	ctx, cancel := gitCommandContext()
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("git command context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > gitCommandTimeout {
		t.Fatalf("git command deadline in %v, want (0, %v]", remaining, gitCommandTimeout)
	}

	// The bound composes with CLI cancellation rather than replacing it.
	parent, parentCancel := context.WithCancel(context.Background())
	previous := CommandContext()
	SetCommandContext(parent)
	t.Cleanup(func() { SetCommandContext(previous); parentCancel() })
	child, childCancel := gitCommandContext()
	defer childCancel()
	parentCancel()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("git context error after parent cancellation = %v, want context.Canceled", child.Err())
	}
}

// TestCloneBridgeRefusesSymlinkedCachePath checks the cache path before any
// clone is attempted. A planted symlink used to let git write outside the
// cache, while embedded materialization and metadata writes already refused it.
func TestCloneBridgeRefusesSymlinkedCachePath(t *testing.T) {
	// appdir's base override is a package global — no t.Parallel here.
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	entry := cacheEntry("symlink-cache", "owner/symlink-cache")
	cacheDir := CachePath(entry)
	if err := os.MkdirAll(CacheDir(), 0o755); err != nil {
		t.Fatalf("mkdir cache root: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.Symlink(outside, cacheDir); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	err := prepareCloneCacheDir(cacheDir)
	if err == nil {
		t.Fatal("preparation accepted a symlinked cache path")
	}
	if !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "escape") {
		t.Errorf("error = %q, want it to name the symlink refusal", err)
	}
}
