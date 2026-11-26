package bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// gitAvailable reports whether git is installed (bridge update handling needs it).
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// testGit runs git in dir and returns combined output, failing the test on error.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitScrubbedEnv(), "GIT_AUTHOR_NAME=domaincraft-test", "GIT_AUTHOR_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeBridgeRepo creates a non-bare git repo at dir with a bridge.yaml committed
// on the given branch. Returns the repo path.
func makeBridgeRepo(t *testing.T, dir, branch string) {
	t.Helper()
	testGit(t, dir, "init", "-q", "-b", branch)
	testGit(t, dir, "config", "user.email", "test@domaincraft.dev")
	testGit(t, dir, "config", "user.name", "DomainCraft Test")
	writeBridgeYAML(t, dir, "1.0.0")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-q", "-m", "init")
}

// newRemoteWithBridge builds a bare git remote with one bridge commit and
// returns the remote path plus the source checkout. Tests drive the unusual
// steps (tags, version bumps, second pushes) through the returned src path.
func newRemoteWithBridge(t *testing.T, root, branch, version string) (remote, src string) {
	t.Helper()
	remote = filepath.Join(root, "remote.git")
	testGit(t, root, "init", "--bare", "-b", branch, "remote.git")
	testGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	src = filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBridgeRepo(t, src, branch)
	if version != "" && version != "1.0.0" {
		writeBridgeYAML(t, src, version)
		testGit(t, src, "add", ".")
		testGit(t, src, "commit", "-q", "-m", "version "+version)
	}
	testGit(t, src, "remote", "add", "origin", remote)
	testGit(t, src, "push", "-q", "-u", "origin", branch)
	return remote, src
}

// writeBridgeYAML writes a minimal bridge.yaml with the given version.
func writeBridgeYAML(t *testing.T, dir, version string) {
	t.Helper()
	body := "name: test-bridge\nversion: \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write bridge.yaml: %v", err)
	}
}

// githubAPIStub is the offline stand-in for the GitHub probes. Calls counts
// every request, so tests can assert the remote was never contacted.
type githubAPIStub struct {
	calls atomic.Int32
	sha   string
	yaml  string
}

// stubGitHubAPI swaps httpGetBodyFn for a deterministic GitHub API response:
// sha for commit probes, yaml for raw bridge.yaml probes (an empty yaml means
// the manifest is unavailable). The original function is restored on cleanup.
func stubGitHubAPI(t *testing.T, sha, yaml string) *githubAPIStub {
	t.Helper()
	stub := &githubAPIStub{sha: sha, yaml: yaml}
	original := httpGetBodyFn
	t.Cleanup(func() { httpGetBodyFn = original })
	httpGetBodyFn = func(url string) ([]byte, error) {
		stub.calls.Add(1)
		switch {
		case strings.Contains(url, "/commits/"):
			return []byte(`{"sha":"` + stub.sha + `"}`), nil
		case strings.Contains(url, "/repos/"):
			return []byte(`{"default_branch":"main"}`), nil
		case strings.Contains(url, "bridge.yaml"):
			if stub.yaml == "" {
				return nil, os.ErrNotExist
			}
			return []byte(stub.yaml), nil
		}
		return nil, os.ErrNotExist
	}
	return stub
}

func (s *githubAPIStub) Calls() int { return int(s.calls.Load()) }

// cacheEntry returns a RegistryEntry whose cache path is under the test cache root.
func cacheEntry(id, source string) RegistryEntry {
	return RegistryEntry{ID: id, GitHub: source}
}
