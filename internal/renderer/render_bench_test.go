package renderer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

// BenchmarkRenderWarm measures regeneration where every target already exists.
// That is the common case for a warm rebuild and used to read every prior file
// into memory solely to support rollback.
func BenchmarkRenderWarm(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("entities=%d", size), func(b *testing.B) {
			dir := b.TempDir()
			bridgeDir := filepath.Join(dir, "bridge")
			if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
				b.Fatal(err)
			}
			manifest := `name: benchmark
contract_version: 1
templates:
  - for: entity
    source: templates/entity.tmpl
    target: "src/{{ .Entity.Name }}.txt"
`
			if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), []byte(manifest), 0o644); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "entity.tmpl"), []byte("generated {{ .Entity.Name }}\n"), 0o644); err != nil {
				b.Fatal(err)
			}
			out := filepath.Join(dir, "out")
			project := &ir.Project{Name: "Bench"}
			for i := range size {
				name := fmt.Sprintf("E%04d", i)
				project.Entities = append(project.Entities, ir.Entity{Name: name, NamePlural: name + "s"})
				target := filepath.Join(out, "src", name+".txt")
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("previous\n"), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			r, err := New(bridgeDir, nil)
			if err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				if _, _, err := r.Render(project, out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSafeOutputPathWarm isolates the symlink-containment work that
// BenchmarkRenderWarm performs twice per file (before and after MkdirAll).
// Measured on Windows/amd64 at 2000 entities: containment 213 ms for 2000
// calls (~106 us/call, x2 per file) vs 3.80 s total render — about 11%.
// An os.Root rewrite removed the component walks but made warm render 2.9x
// slower there, because every Root operation re-resolves each component through
// directory handles. The first pass is now cached per Render (directories
// only); the second stays fresh. A/B at 2000 entities: 4.32 s -> 4.17 s median
// warm render. Keep this benchmark as the measurement baseline.
func BenchmarkSafeOutputPathWarm(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("entities=%d", size), func(b *testing.B) {
			out := filepath.Join(b.TempDir(), "out")
			targets := make([]string, 0, size)
			for i := range size {
				rel := fmt.Sprintf("src/E%04d.txt", i)
				target := filepath.Join(out, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("previous\n"), 0o644); err != nil {
					b.Fatal(err)
				}
				targets = append(targets, rel)
			}
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
