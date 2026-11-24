package bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// writeBridgeYAML writes a minimal bridge.yaml with the given version.
func writeBridgeYAML(t *testing.T, dir, version string) {
	t.Helper()
	body := "name: test-bridge\nversion: \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write bridge.yaml: %v", err)
	}
}

// cacheEntry returns a RegistryEntry whose cache path is under the test cache root.
func cacheEntry(id, source string) RegistryEntry {
	return RegistryEntry{ID: id, GitHub: source}
}
