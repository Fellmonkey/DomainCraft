package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

// writeBridgeDir scaffolds a minimal bridge directory (just a bridge.yaml,
// optionally declaring `extends:`) and returns its absolute path.
func writeBridgeDir(t *testing.T, dir, extends string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "name: " + filepath.Base(dir) + "\n"
	if extends != "" {
		content += "extends: " + extends + "\n"
	}
	path := filepath.Join(dir, "bridge.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return dir
}

// writeLayeredBridge scaffolds a bridge with an explicit layer (and optional
// extends) so --replace can be exercised without cloning anything.
func writeLayeredBridge(t *testing.T, dir, layer, extends string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "name: " + filepath.Base(dir) + "\nlayer: " + layer + "\n"
	if extends != "" {
		content += "extends: " + extends + "\n"
	}
	path := filepath.Join(dir, "bridge.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return dir
}

// layeredChain builds core <- domain <- persistence <- transport plus an
// alternative bridge used as a --replace target.
func layeredChain(t *testing.T, altLayer, altExtends string) (core, domain, persistence, transport, alt string) {
	t.Helper()
	root := t.TempDir()
	core = writeLayeredBridge(t, filepath.Join(root, "c-core"), "core", "")
	domain = writeLayeredBridge(t, filepath.Join(root, "c-domain"), "domain", core)
	persistence = writeLayeredBridge(t, filepath.Join(root, "c-efcore"), "persistence", domain)
	transport = writeLayeredBridge(t, filepath.Join(root, "c-rest"), "transport", persistence)
	if altLayer == "" {
		altLayer = "persistence"
	}
	if altExtends == "" {
		altExtends = domain
	}
	alt = writeLayeredBridge(t, filepath.Join(root, "c-dapper"), altLayer, altExtends)
	return
}

func TestParseReplaceFlags(t *testing.T) {
	t.Run("valid entries are trimmed and split once", func(t *testing.T) {
		got, err := parseReplaceFlags([]string{
			"persistence=../dapper",
			" csharp-efcore = bridges/csharp/persistence/efcore ",
			"persistence=/tmp/a=b",
		})
		if err != nil {
			t.Fatalf("parseReplaceFlags: %v", err)
		}
		want := []replaceEntry{
			{left: "persistence", right: "../dapper"},
			{left: "csharp-efcore", right: "bridges/csharp/persistence/efcore"},
			{left: "persistence", right: "/tmp/a=b"},
		}
		if len(got) != len(want) {
			t.Fatalf("entries = %+v, want %+v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("malformed entries are rejected", func(t *testing.T) {
		for _, raw := range []string{"persistence", "=../dapper", "persistence=", ""} {
			if _, err := parseReplaceFlags([]string{raw}); err == nil {
				t.Errorf("parseReplaceFlags(%q) should fail", raw)
			}
		}
	})
}

func TestResolveChainWithReplacements(t *testing.T) {
	checkBases := func(t *testing.T, got []string, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("bases = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("base %d = %q, want %q", i, got[i], want[i])
			}
		}
	}

	t.Run("replacement by layer splices the chain", func(t *testing.T) {
		core, domain, _, transport, alt := layeredChain(t, "", "")
		var logOut bytes.Buffer
		log := logger.New()
		log.SetWriter(&logOut)
		entry, bases, err := resolveChainWithReplacements(&cliOptions{}, transport,
			[]replaceEntry{{left: "persistence", right: alt}}, log)
		if err != nil {
			t.Fatalf("resolveChainWithReplacements: %v", err)
		}
		if entry != transport {
			t.Errorf("entry = %q, want %q", entry, transport)
		}
		checkBases(t, bases, []string{core, domain, alt})
	})

	t.Run("replacement by bridgeId finds the same node", func(t *testing.T) {
		core, domain, _, transport, alt := layeredChain(t, "", "")
		quiet := logger.New()
		quiet.SetWriter(&bytes.Buffer{})
		entry, bases, err := resolveChainWithReplacements(&cliOptions{}, transport,
			[]replaceEntry{{left: "c-efcore", right: alt}}, quiet)
		if err != nil {
			t.Fatalf("resolveChainWithReplacements: %v", err)
		}
		if entry != transport {
			t.Errorf("entry = %q, want %q", entry, transport)
		}
		checkBases(t, bases, []string{core, domain, alt})
	})

	t.Run("layer mismatch warns but still succeeds", func(t *testing.T) {
		_, _, _, transport, alt := layeredChain(t, "storage", "")
		var logOut bytes.Buffer
		log := logger.New()
		log.SetWriter(&logOut)
		if _, _, err := resolveChainWithReplacements(&cliOptions{}, transport,
			[]replaceEntry{{left: "persistence", right: alt}}, log); err != nil {
			t.Fatalf("resolveChainWithReplacements: %v", err)
		}
		if !strings.Contains(logOut.String(), "layer mismatch") || !strings.Contains(logOut.String(), "c-dapper") {
			t.Errorf("warning = %q, want the layer mismatch and replacement name", logOut.String())
		}
	})

	t.Run("duplicate layer is rejected", func(t *testing.T) {
		_, _, _, transport, alt := layeredChain(t, "domain", "")
		quiet := logger.New()
		quiet.SetWriter(&bytes.Buffer{})
		_, _, err := resolveChainWithReplacements(&cliOptions{}, transport,
			[]replaceEntry{{left: "persistence", right: alt}}, quiet)
		if err == nil || !strings.Contains(err.Error(), "duplicate layer") {
			t.Fatalf("err = %v, want duplicate layer error", err)
		}
	})

	t.Run("cycle is rejected", func(t *testing.T) {
		_, _, _, transport, _ := layeredChain(t, "", "")
		quiet := logger.New()
		quiet.SetWriter(&bytes.Buffer{})
		_, _, err := resolveChainWithReplacements(&cliOptions{}, transport,
			[]replaceEntry{{left: "domain", right: transport}}, quiet)
		if err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want cycle error", err)
		}
	})
}

func TestResolveBaseChainOrdersOutermostBaseFirst(t *testing.T) {
	root := t.TempDir()

	c := writeBridgeDir(t, filepath.Join(root, "bridge-c"), "")
	b := writeBridgeDir(t, filepath.Join(root, "bridge-b"), c)
	a := writeBridgeDir(t, filepath.Join(root, "bridge-a"), b)

	chain, err := resolveBaseChain(&cliOptions{}, a, b, logger.New())
	if err != nil {
		t.Fatalf("resolveBaseChain: %v", err)
	}

	if len(chain) != 2 {
		t.Fatalf("chain = %v, want the two bases (the adapter itself is not part of the chain)", chain)
	}
	if chain[0] != c || chain[1] != b {
		t.Errorf("chain = %v, want [%s %s] (deepest base first)", chain, c, b)
	}
}

func TestResolveBaseChainDetectsCycle(t *testing.T) {
	root := t.TempDir()

	b := writeBridgeDir(t, filepath.Join(root, "bridge-b"), "")
	a := writeBridgeDir(t, filepath.Join(root, "bridge-a"), b)
	// Close the cycle: B now extends A while A extends B.
	writeBridgeDir(t, filepath.Join(root, "bridge-b"), a)

	_, err := resolveBaseChain(&cliOptions{}, a, b, logger.New())
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("err = %v, want it to mention the cycle", err)
	}
}

func TestResolveExtendsRefPrefersLocalCheckout(t *testing.T) {
	root := t.TempDir()
	resolver := bridge.NewResolver(bridge.Default())

	// Registry ID whose repo checkout sits next to the extending bridge
	// (monorepo sibling layout) — must resolve locally without cloning.
	sibling := writeBridgeDir(t, filepath.Join(root, "domaincraft-bridge-csharp"), "")

	got, err := bridge.ResolveExtendsRef("csharp-restful", filepath.Join(root, "adapter"), resolver)
	if err != nil {
		t.Fatalf("resolveExtendsRef: %v", err)
	}
	if abs, _ := filepath.Abs(got); abs != sibling {
		t.Errorf("resolved = %q, want the sibling checkout %q", got, sibling)
	}

	// CI layout: the base bridge checked out inside the adapter's directory.
	innerRoot := t.TempDir()
	innerAdapter := filepath.Join(innerRoot, "adapter")
	inner := writeBridgeDir(t, filepath.Join(innerAdapter, "domaincraft-bridge-csharp"), "")

	got, err = bridge.ResolveExtendsRef("csharp-restful", innerAdapter, resolver)
	if err != nil {
		t.Fatalf("resolveExtendsRef: %v", err)
	}
	if abs, _ := filepath.Abs(got); abs != inner {
		t.Errorf("resolved = %q, want the in-directory checkout %q", got, inner)
	}
}

func TestResolveExtendsRefPrefersMonorepoLayout(t *testing.T) {
	root := t.TempDir()
	resolver := bridge.NewResolver(bridge.Default())

	// Monorepo layout: <root>/bridges/csharp/core for the csharp-core bridge.
	mp := writeBridgeDir(t, filepath.Join(root, "bridges", "csharp", "core"), "")

	// Distractor sibling checkout with the legacy repo name — must NOT win now.
	writeBridgeDir(t, filepath.Join(root, "domaincraft-bridge-csharp-core"), "")

	got, err := bridge.ResolveExtendsRef("csharp-core", filepath.Join(root, "adapter"), resolver)
	if err != nil {
		t.Fatalf("resolveExtendsRef: %v", err)
	}
	if abs, _ := filepath.Abs(got); abs != mp {
		t.Errorf("resolved = %q, want the monorepo bridge %q", got, mp)
	}

	// A bridge with a multi-segment path: csharp-efcore → csharp/persistence/efcore.
	ef := writeBridgeDir(t, filepath.Join(root, "bridges", "csharp", "persistence", "efcore"), "")
	got, err = bridge.ResolveExtendsRef("csharp-efcore", filepath.Join(root, "adapter"), resolver)
	if err != nil {
		t.Fatalf("bridge.ResolveExtendsRef(efcore): %v", err)
	}
	if abs, _ := filepath.Abs(got); abs != ef {
		t.Errorf("resolved = %q, want %q", got, ef)
	}
}

func TestResolveExtendsRefFallsBackWhenNoMonorepo(t *testing.T) {
	root := t.TempDir()
	resolver := bridge.NewResolver(bridge.Default())
	sibling := writeBridgeDir(t, filepath.Join(root, "domaincraft-bridge-csharp"), "")
	got, err := bridge.ResolveExtendsRef("csharp-restful", filepath.Join(root, "adapter"), resolver)
	if err != nil {
		t.Fatalf("resolveExtendsRef: %v", err)
	}
	if abs, _ := filepath.Abs(got); abs != sibling {
		t.Errorf("resolved = %q, want legacy sibling %q", got, sibling)
	}
}

func TestBridgeUpdateNoticeOmitsCachedVersion(t *testing.T) {
	cases := []struct {
		name string
		u    *bridge.Update
	}{
		{"nil update", nil},
		{"unknown local version", &bridge.Update{}},
		{"known local version", &bridge.Update{LocalVersion: "1.2.3"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bridgeUpdateNotice("csharp-core", c.u)
			if !strings.Contains(got, "Updating bridge csharp-core") {
				t.Errorf("notice = %q, want the bridge ID", got)
			}
			if strings.Contains(got, "1.2.3") || strings.Contains(got, "available") {
				t.Errorf("notice = %q, must not present the cached version as available", got)
			}
		})
	}
}
