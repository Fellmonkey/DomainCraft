// Package contract hosts the core-level contract tests for the bundled
// bridges. Phase 2: a golden output test plus a determinism check.
package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/bridgechain"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/packages"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/seed"
	"github.com/DomainCraft/DomainCraft/internal/validator"
)

var updateGolden = flag.Bool("update", false, "regenerate golden files")

// goldenBridges are the official bridges with committed golden manifests: the
// self-contained leaves plus the composed chains (csharp-rest → … →
// csharp-core, react-rest → … → ts-core). Composed chains are resolved by the
// same importable bridgechain.Walk the CLI uses and rendered base-first, so
// their manifests pin the real composition order.
var goldenBridges = []string{
	"csharp-core",
	"ts-core",
	"admin-alpine",
	"appwrite",
	"csharp-rest",
	"react-rest",
}

// hermeticPackageVersion is the fixed version seeded into the per-bridge
// package cache in TestMain. Composed C# bridges declare NuGet packages; the
// seeded cache keeps rendering offline and makes the golden manifests stable
// even when upstream versions change.
const hermeticPackageVersion = "1.0.0"

type goldenBridgeManifest struct {
	Name             string            `yaml:"name"`
	RegistryURL      string            `yaml:"registry_url"`
	RegistryPackages map[string]string `yaml:"registry_packages"`
	TargetFramework  string            `yaml:"target_framework"`
}

type goldenPackageCache struct {
	Entries map[string]goldenPackageEntry `json:"entries"`
}

type goldenPackageEntry struct {
	Value    string    `json:"value"`
	StoredAt time.Time `json:"stored_at"`
}

// TestMain points per-user state at a temporary directory and seeds the
// package-version caches of every bundled bridge. No registry is contacted by
// any test in this package.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "domaincraft-golden-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "golden temp dir:", err)
		os.Exit(1)
	}
	appdir.SetBaseForTesting(root)
	code := 0
	framework, err := goldenPlatform()
	if err == nil {
		err = seedPackageVersions(framework)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed package versions:", err)
		code = 1
	} else {
		code = m.Run()
	}
	appdir.SetBaseForTesting("")
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// goldenPlatform returns the target platform of the golden domain model. It is
// part of the package-version cache key and must match the framework the
// renderer sees.
func goldenPlatform() (string, error) {
	data, err := os.ReadFile(filepath.Join("testdata", "domain.yaml"))
	if err != nil {
		return "", err
	}
	schema, err := parser.ParseYAML(data)
	if err != nil {
		return "", err
	}
	proj, err := ir.Build(schema)
	if err != nil {
		return "", err
	}
	return proj.Platform, nil
}

// cacheNamespace mirrors renderer.cacheNamespace: package versions are cached
// under the adapter bridge's name, not under its filesystem path.
func cacheNamespace(name string) string {
	var b strings.Builder
	for _, ch := range strings.ToLower(strings.TrimSpace(name)) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func readBridgeManifest(dir string) (goldenBridgeManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bridge.yaml"))
	if err != nil {
		return goldenBridgeManifest{}, err
	}
	var m goldenBridgeManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return goldenBridgeManifest{}, fmt.Errorf("parse %s: %w", dir, err)
	}
	return m, nil
}

// seedPackageVersions writes one packages.json per bundled adapter so the
// renderer never needs a network lookup. The key layout is the one documented
// by internal/packages: registryURL + NUL + packageID + NUL + framework.
func seedPackageVersions(framework string) error {
	resolver := bridge.NewResolver(bridge.Default())
	for _, entry := range bridge.Default().All() {
		if entry.Path == "" || entry.Deprecated {
			continue
		}
		dir, err := filepath.Abs(filepath.Join("..", "..", "bridges", filepath.FromSlash(entry.Path)))
		if err != nil {
			return err
		}
		chain, err := bridgechain.Walk(dir, dir, resolver, nil)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", entry.ID, err)
		}
		// The renderer merges registry_packages from the whole chain under a
		// single URL: the adapter's, otherwise the first one declared by a base.
		packageIDs := make(map[string]struct{})
		registryURL := ""
		var adapter goldenBridgeManifest
		for i, base := range chain {
			m, err := readBridgeManifest(base)
			if err != nil {
				return err
			}
			if i == 0 {
				adapter = m
			}
			for _, packageID := range m.RegistryPackages {
				packageIDs[packageID] = struct{}{}
			}
			if registryURL == "" && m.RegistryURL != "" {
				registryURL = m.RegistryURL
			}
		}
		if len(packageIDs) == 0 {
			continue
		}
		if registryURL == "" {
			return fmt.Errorf("bridge %s declares packages but no registry_url", entry.ID)
		}
		// The renderer uses the project platform when set, otherwise the
		// bridge-declared target_framework. Seed both keys so a golden run is
		// hermetic even when the golden domain declares no platform.
		frameworks := map[string]bool{framework: true}
		for _, base := range chain {
			m, err := readBridgeManifest(base)
			if err != nil {
				return err
			}
			if m.TargetFramework != "" {
				frameworks[m.TargetFramework] = true
			}
		}
		packagesByKey := make(map[string]goldenPackageEntry, len(packageIDs)*len(frameworks))
		storedAt := time.Now()
		for packageID := range packageIDs {
			for fw := range frameworks {
				key := registryURL + "\x00" + packageID + "\x00" + fw
				packagesByKey[key] = goldenPackageEntry{Value: hermeticPackageVersion, StoredAt: storedAt}
			}
		}
		cacheDir, err := appdir.CacheDir(cacheNamespace(adapter.Name))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return err
		}
		data, err := json.Marshal(goldenPackageCache{Entries: packagesByKey})
		if err != nil {
			return err
		}
		path := filepath.Join(cacheDir, packages.CacheFileName)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func loadGoldenProject(t *testing.T) *ir.Project {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "domain.yaml"))
	if err != nil {
		t.Fatalf("read testdata/domain.yaml: %v", err)
	}
	schema, err := parser.ParseYAML(data)
	if err != nil {
		t.Fatalf("parse domain.yaml: %v", err)
	}
	for _, ve := range validator.New(schema).Validate() {
		if !ve.Warning {
			t.Fatalf("validation error: %s", ve.Error())
		}
	}
	proj, err := ir.Build(schema)
	if err != nil {
		t.Fatalf("build IR: %v", err)
	}
	return proj
}

func bridgeDir(t *testing.T, id string) string {
	t.Helper()
	entry := bridge.Default().ByID(id)
	if entry == nil || entry.Path == "" {
		t.Fatalf("bridge %q not in registry or has no monorepo path", id)
	}
	return filepath.Join("..", "..", "bridges", filepath.FromSlash(entry.Path))
}

// resolveChain resolves a bridge's full `extends` chain top-first (adapter
// first, deepest base last) through the same importable walker the CLI uses.
func resolveChain(t *testing.T, entryDir string) []string {
	t.Helper()
	abs, err := filepath.Abs(entryDir)
	if err != nil {
		t.Fatalf("abs(%s): %v", entryDir, err)
	}
	chain, err := bridgechain.Walk(abs, abs, bridge.NewResolver(bridge.Default()), nil)
	if err != nil {
		t.Fatalf("bridgechain.Walk(%s): %v", abs, err)
	}
	return chain
}

// renderChain renders proj through a resolved bridge chain (top-first) and
// returns a path→sha256 map of the produced files (relative to the output dir,
// forward slashes).
func renderChain(t *testing.T, chain []string, proj *ir.Project) map[string]string {
	t.Helper()
	out := t.TempDir()
	r, err := renderer.New(chain[0], nil)
	if err != nil {
		t.Fatalf("renderer.New(%s): %v", chain[0], err)
	}
	if len(chain) > 1 {
		// Walk returns top-first; AttachBases wants the outermost base first,
		// exactly as the CLI's resolveBaseChain does.
		bases := slices.Clone(chain[1:])
		slices.Reverse(bases)
		if err := r.AttachBases(bases); err != nil {
			t.Fatalf("AttachBases(%v): %v", bases, err)
		}
	}
	r.SetSeedData(seed.Normalize(proj))
	r.SetMockData(seed.Generate(proj, seed.Options{}))
	if _, _, err := r.Render(proj, out); err != nil {
		t.Fatalf("Render(%s): %v", chain[0], err)
	}
	files := make(map[string]string)
	if err := filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(out, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Hash normalized content so the golden manifest is platform-independent:
		// a Windows checkout yields CRLF while Linux yields LF, and the bytes
		// should compare equal regardless of the host's line endings.
		b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		sum := sha256.Sum256(b)
		files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	}); err != nil {
		t.Fatalf("walk output: %v", err)
	}
	return files
}

func manifestLines(m map[string]string) []string {
	keys := slices.Sorted(maps.Keys(m))
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s\t%s", k, m[k]))
	}
	return lines
}

// TestGoldenLeafBridges asserts that every hermetic bridge — leaves and
// composed chains alike — produces byte-identical output to a committed golden
// manifest. Regenerate changed output with `go test ./internal/contract -update`.
func TestGoldenLeafBridges(t *testing.T) {
	t.Parallel()
	proj := loadGoldenProject(t)
	for _, id := range goldenBridges {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(manifestLines(renderChain(t, resolveChain(t, bridgeDir(t, id)), proj)), "\n")
			goldenPath := filepath.Join("testdata", id+".golden.txt")
			if *updateGolden {
				if err := os.WriteFile(goldenPath, []byte(got+"\n"), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			data, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s (run with -update to create): %v", goldenPath, err)
			}
			// Normalize the stored golden the same way the output is normalized, so
			// a CRLF checkout on Windows compares equal to an LF checkout on Linux.
			data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
			if want := strings.TrimRight(string(data), "\n"); want != got {
				t.Errorf("generated output for %s differs from golden manifest", id)
			}
		})
	}
}

// TestGoldenSeedCoversBridgeTargetFramework guards the hermetic cache: the
// renderer resolves packages with the bridge-declared TFM when the domain has
// no platform, so that key must be seeded or golden runs hit the network.
func TestGoldenSeedCoversBridgeTargetFramework(t *testing.T) {
	cacheDir, err := appdir.CacheDir(cacheNamespace("csharp-rest"))
	if err != nil {
		t.Fatalf("resolve seeded cache dir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cacheDir, packages.CacheFileName))
	if err != nil {
		t.Fatalf("read seeded package cache: %v", err)
	}
	var cache goldenPackageCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("decode seeded package cache: %v", err)
	}
	want := "\x00net10.0"
	for key := range cache.Entries {
		if strings.HasSuffix(key, want) {
			return
		}
	}
	t.Fatalf("seeded cache has no %q key (keys: %d)", want, len(cache.Entries))
}

// TestRenderDeterministic asserts that two renders of the same domain through
// the same bridge chain produce byte-identical trees (the core determinism
// invariant). It covers every bundled bridge, including composed extends-chain
// bridges. The chain is resolved through the same importable resolver the CLI
// uses.
func TestRenderDeterministic(t *testing.T) {
	t.Parallel()
	proj := loadGoldenProject(t)
	for _, entry := range bridge.Default().All() {
		if entry.Path == "" || entry.Deprecated {
			continue
		}
		id := entry.ID
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			chain := resolveChain(t, bridgeDir(t, id))
			a := strings.Join(manifestLines(renderChain(t, chain, proj)), "\n")
			b := strings.Join(manifestLines(renderChain(t, chain, proj)), "\n")
			if a != b {
				t.Errorf("bridge %s produced non-deterministic output across two runs", id)
			}
		})
	}
}
