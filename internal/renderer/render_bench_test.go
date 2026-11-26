package renderer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

// benchmarkRenderFixture builds the bridge, project and pre-existing output
// tree shared by BenchmarkRenderWarm and TestBenchmarkRenderWarmGolden. One
// fixture keeps the golden digest tied to exactly what the benchmark measures.
func benchmarkRenderFixture(tb testing.TB, size int) (*Renderer, string, *ir.Project) {
	tb.Helper()
	dir := tb.TempDir()
	bridgeDir := filepath.Join(dir, "bridge")
	if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
		tb.Fatal(err)
	}
	manifest := `name: benchmark
contract_version: 1
templates:
  - for: entity
    source: templates/entity.tmpl
    target: "src/{{ .Entity.Name }}.txt"
`
	if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), []byte(manifest), 0o644); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "entity.tmpl"), []byte("generated {{ .Entity.Name }}\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	project := &ir.Project{Name: "Bench"}
	for i := range size {
		name := fmt.Sprintf("E%04d", i)
		project.Entities = append(project.Entities, ir.Entity{Name: name, NamePlural: name + "s"})
		target := filepath.Join(out, "src", name+".txt")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("previous\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	r, err := New(bridgeDir, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return r, out, project
}

// BenchmarkRenderWarm measures regeneration where every target already exists.
// That is the common case for a warm rebuild and used to read every prior file
// into memory solely to support rollback.
func BenchmarkRenderWarm(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("entities=%d", size), func(b *testing.B) {
			r, out, project := benchmarkRenderFixture(b, size)
			for b.Loop() {
				if _, _, err := r.Render(project, out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchmarkSafePathFixture builds the output tree shared by
// BenchmarkSafeOutputPathWarm, its golden test and its allocation guard.
func benchmarkSafePathFixture(tb testing.TB, size int) (string, []string) {
	tb.Helper()
	out := filepath.Join(tb.TempDir(), "out")
	targets := make([]string, 0, size)
	for i := range size {
		rel := fmt.Sprintf("src/E%04d.txt", i)
		target := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("previous\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
		targets = append(targets, rel)
	}
	return out, targets
}

// BenchmarkSafeOutputPathWarm isolates the symlink-containment work that
// BenchmarkRenderWarm performs twice per file (before and after MkdirAll).
// Measured on Windows/amd64 at 2000 entities: containment 213 ms for 2000
// calls (~106 us/call, x2 per file) vs 3.80 s total render — about 11%.
// The first pass is cached per Render (directories only); the second stays
// fresh. A/B at 2000 entities: 4.32 s -> 4.17 s median warm render.
//
// A full os.Root rewrite of staging + commit is NOT acceptable: every Root
// operation re-resolves each path component through directory handles, which
// measured 10.43 s/op vs 3.65 s/op baseline (2.86x). The commit-phase fix
// instead captures each parent directory once during staging, opens one
// os.Root per parent at commit, and routes only the two rename calls through
// it (single-component names): 4.14 s/op (1.13x). Do not move staging file
// creation, Lstat or containment checks onto Root — that reintroduces the
// 2.9x regression. Keep this benchmark as the measurement baseline.
func BenchmarkSafeOutputPathWarm(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("entities=%d", size), func(b *testing.B) {
			out, targets := benchmarkSafePathFixture(b, size)
			for b.Loop() {
				for _, rel := range targets {
					if _, err := safeOutputPath(out, rel); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

