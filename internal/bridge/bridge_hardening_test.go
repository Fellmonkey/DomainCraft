package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		origin string
		want   bool
	}{
		{origin: "https://github.com/owner/repo.git", want: true},
		{origin: "git@github.com:owner/repo.git", want: true},
		{origin: "https://github.com/owner/repo-evil.git", want: false},
		{origin: "", want: false},
	} {
		if got := githubOriginMatches(c.origin, "owner/repo"); got != c.want {
			t.Errorf("githubOriginMatches(%q) = %t, want %t", c.origin, got, c.want)
		}
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
