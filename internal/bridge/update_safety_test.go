package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

// makeMonorepoBridge creates <root>/bridges/<rel>/bridge.yaml and marks root as
// the DomainCraft monorepo with a go.mod, matching the real checkout layout.
func makeMonorepoBridge(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, "bridges", filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte("name: csharp-core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/DomainCraft/DomainCraft\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestIsOfficialBundled distinguishes bundled official bridges (which may
// auto-run declared migrations) from community/local bridges (which must not).
func TestIsOfficialBundled(t *testing.T) {
	root := t.TempDir()
	official := makeMonorepoBridge(t, root, "csharp/core")
	if !IsOfficialBundled(official) {
		t.Errorf("IsOfficialBundled(%s) = false, want true", official)
	}

	// A custom/local path must not count as bundled official.
	custom := filepath.Join(root, "my-bridge")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if IsOfficialBundled(custom) {
		t.Errorf("IsOfficialBundled(%s) = true, want false", custom)
	}

	// A registry cache path (~/.domaincraft/bridges/<id>) must not count either.
	cache := filepath.Join(root, "csharp-core")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if IsOfficialBundled(cache) {
		t.Errorf("IsOfficialBundled(%s) = true, want false", cache)
	}

	// Regression: `mybridges` ends with `bridges` but is not a path component
	// boundary. The old HasSuffix check treated it as bundled-official and
	// bypassed the community-bridge migration consent gate.
	lookalike := filepath.Join(root, "mybridges", "csharp", "core")
	if err := os.MkdirAll(lookalike, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lookalike, "bridge.yaml"), []byte("name: evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsOfficialBundled(lookalike) {
		t.Errorf("IsOfficialBundled(%s) = true for a lookalike path, want false", lookalike)
	}
}

// TestMonorepoRelPathRejectsLookalikes pins the three checks behind the
// official-bundled decision: component boundary, embedded membership and the
// real monorepo root.
func TestMonorepoRelPathRejectsLookalikes(t *testing.T) {
	root := t.TempDir()
	realBridge := makeMonorepoBridge(t, root, "csharp/core")

	other := t.TempDir()
	noMarker := filepath.Join(other, "bridges", "csharp", "core")
	if err := os.MkdirAll(noMarker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noMarker, "bridge.yaml"), []byte("name: csharp-core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lookalike := filepath.Join(other, "mybridges", "csharp", "core")
	if err := os.MkdirAll(lookalike, 0o755); err != nil {
		t.Fatal(err)
	}
	unbundled := makeMonorepoBridge(t, t.TempDir(), "custom/bridge")

	tests := []struct {
		name string
		dir  string
		path string
		want string
	}{
		{name: "real monorepo bridge", dir: realBridge, path: "csharp/core", want: "bridges/csharp/core"},
		{name: "lookalike component", dir: lookalike, path: "csharp/core"},
		{name: "no monorepo root", dir: noMarker, path: "csharp/core"},
		{name: "not embedded in binary", dir: unbundled, path: "custom/bridge"},
		{name: "traversal in entry path", dir: realBridge, path: "../../csharp/core"},
		{name: "empty entry path", dir: realBridge, path: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MonorepoRelPath(tt.dir, tt.path)
			if got != tt.want {
				t.Errorf("MonorepoRelPath(%q, %q) = %q, want %q", tt.dir, tt.path, got, tt.want)
			}
		})
	}
}

// TestSplitPinRef covers the community-bridge pin syntax `owner/repo@ref`.
func TestSplitPinRef(t *testing.T) {
	cases := []struct {
		name string
		in   string
		repo string
		ref  string
	}{
		{"plain", "owner/repo", "owner/repo", ""},
		{"registry id stays unpinned", "csharp-rest", "csharp-rest", ""},
		{"version_pin", "owner/repo@v1.2.3", "owner/repo", "v1.2.3"},
		{"commit_pin", "owner/repo@9f8e7d6c5b4a3210", "owner/repo", "9f8e7d6c5b4a3210"},
		{"branch_pin", "owner/repo@feature/branch", "owner/repo", "feature/branch"},
		{"last_at_wins", "owner/repo@tag@x", "owner/repo@tag", "x"}, // last @ wins
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, ref := splitPinRef(c.in)
			if repo != c.repo || ref != c.ref {
				t.Errorf("splitPinRef(%q) = (%q, %q), want (%q, %q)", c.in, repo, ref, c.repo, c.ref)
			}
		})
	}
}
