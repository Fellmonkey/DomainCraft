// Package contract hosts the core-level contract tests for the bundled
// bridges. Phase 2: a golden output test plus a determinism check.
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/seed"
	"github.com/DomainCraft/DomainCraft/internal/validator"
)

var updateGolden = flag.Bool("update", false, "regenerate golden files")

// leafBridges are the self-contained (no `extends`) official bridges that
// render without network package resolution — the hermetic subject of the
// golden test. Composed bridges (e.g. csharp-rest → … → csharp-core) need the
// extends-chain resolver, which lives in cmd/domaincraft; covering them here is
// a follow-up once that resolution moves into an importable package.
var leafBridges = []string{"csharp-core", "ts-core", "admin-alpine", "appwrite"}

func loadGoldenProject(t *testing.T) *ir.IRProject {
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

// renderTree renders proj through the bridge and returns a path→sha256 map of
// the produced files (relative to the output dir, forward slashes).
func renderTree(t *testing.T, dir string, proj *ir.IRProject) map[string]string {
	t.Helper()
	out := t.TempDir()
	r, err := renderer.New(dir, nil)
	if err != nil {
		t.Fatalf("renderer.New(%s): %v", dir, err)
	}
	r.SetSeedData(seed.Normalize(proj))
	r.SetMockData(seed.Generate(proj, seed.Options{}))
	if _, _, err := r.Render(proj, out); err != nil {
		t.Fatalf("Render(%s): %v", dir, err)
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
		sum := sha256.Sum256(b)
		files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	}); err != nil {
		t.Fatalf("walk output: %v", err)
	}
	return files
}

func manifestLines(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s\t%s", k, m[k]))
	}
	return lines
}

// TestGoldenLeafBridges asserts that each hermetic leaf bridge produces
// byte-identical output to a committed golden manifest. Regenerate changed
// output with `go test ./internal/contract -update`.
func TestGoldenLeafBridges(t *testing.T) {
	proj := loadGoldenProject(t)
	for _, id := range leafBridges {
		t.Run(id, func(t *testing.T) {
			got := strings.Join(manifestLines(renderTree(t, bridgeDir(t, id), proj)), "\n")
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
			if want := strings.TrimRight(string(data), "\n"); want != got {
				t.Errorf("generated output for %s differs from golden manifest", id)
			}
		})
	}
}

// TestRenderDeterministic asserts that two renders of the same domain through
// the same bridge produce byte-identical trees (the core determinism invariant).
func TestRenderDeterministic(t *testing.T) {
	proj := loadGoldenProject(t)
	for _, id := range leafBridges {
		t.Run(id, func(t *testing.T) {
			a := strings.Join(manifestLines(renderTree(t, bridgeDir(t, id), proj)), "\n")
			b := strings.Join(manifestLines(renderTree(t, bridgeDir(t, id), proj)), "\n")
			if a != b {
				t.Errorf("bridge %s produced non-deterministic output across two runs", id)
			}
		})
	}
}
