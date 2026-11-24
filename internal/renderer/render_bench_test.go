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
			for i := 0; i < size; i++ {
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
