package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsOfficialBundled distinguishes bundled official bridges (which may
// auto-run declared migrations) from community/local bridges (which must not).
func TestIsOfficialBundled(t *testing.T) {
	root := t.TempDir()

	// A bundled official monorepo location: <root>/bridges/csharp/core.
	official := filepath.Join(root, "bridges", "csharp", "core")
	if err := os.MkdirAll(official, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(official, "bridge.yaml"), []byte("name: csharp-core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
}

// TestSplitPinRef covers the community-bridge pin syntax `owner/repo@ref`.
func TestSplitPinRef(t *testing.T) {
	cases := []struct {
		in   string
		repo string
		ref  string
	}{
		{"owner/repo", "owner/repo", ""},
		{"owner/repo@v1.2.3", "owner/repo", "v1.2.3"},
		{"owner/repo@9f8e7d6c5b4a3210", "owner/repo", "9f8e7d6c5b4a3210"},
		{"owner/repo@feature/branch", "owner/repo", "feature/branch"},
		{"owner/repo@tag@x", "owner/repo@tag", "x"}, // last @ wins
	}
	for _, c := range cases {
		repo, ref := splitPinRef(c.in)
		if repo != c.repo || ref != c.ref {
			t.Errorf("splitPinRef(%q) = (%q, %q), want (%q, %q)", c.in, repo, ref, c.repo, c.ref)
		}
	}
}

// TestPinRefNotAppliedToRegistryIDs ensures the pin syntax never collides with
// path handling: a bare registry ID is not treated as owner/repo.
func TestPinRefNotAppliedToRegistryIDs(t *testing.T) {
	if _, ref := splitPinRef("csharp-rest"); ref != "" {
		t.Errorf("registry ID got a ref: %q", ref)
	}
}
