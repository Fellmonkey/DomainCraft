package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
)

// benchmarkDiffData builds an old snapshot with five manifest files per entity
// and a current project where every entity has both a type change and a field
// rename hint, so ComputeDiff visits customEntityFiles on both passes.
func benchmarkDiffData(b *testing.B, entities int, onDisk, custom bool) (string, *Snapshot, *ir.Project) {
	b.Helper()
	dir := b.TempDir()
	oldNamespace := "OldProject"
	if !onDisk {
		// Keep the namespace identical so the benchmark measures only the
		// per-entity change passes, not file content scanning.
		oldNamespace = "NewProject"
	}
	old := &Snapshot{
		FormatVersion:    FormatVersion,
		ProjectNamespace: oldNamespace,
		Files:            make([]renderer.RenderedFile, 0, entities*5),
		Entities:         make(map[string]EntityState, entities),
	}
	project := &ir.Project{
		Name:     "NewProject",
		Entities: make([]ir.Entity, 0, entities),
	}
	for i := range entities {
		name := fmt.Sprintf("E%04d", i)
		for j := range 5 {
			rel := fmt.Sprintf("src/%s/f%d.g.cs", name, j)
			if onDisk {
				abs := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(abs, []byte("generated\n"), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			old.Files = append(old.Files, renderer.RenderedFile{
				Path: rel, Entity: name, Custom: custom && j == 0, Written: true,
			})
		}
		old.Entities[name] = EntityState{
			Fields:        map[string]string{"value": "string", "previous": "uuid"},
			FieldOldNames: map[string]string{"previous": "original"},
		}
		project.Entities = append(project.Entities, ir.Entity{
			Name: name,
			Fields: []ir.Field{
				{Name: "value", DatabaseType: "int"},
				{Name: "current", OldName: "previous", DatabaseType: "uuid", DatabaseColumnName: "current", OldDatabaseColumnName: "previous"},
			},
		})
	}
	return dir, old, project
}

func BenchmarkComputeDiff(b *testing.B) {
	for _, mode := range []struct {
		name   string
		onDisk bool
		custom bool
	}{
		{name: "no-custom", onDisk: false, custom: false},
		{name: "manifest", onDisk: false, custom: true},
		{name: "on-disk", onDisk: true, custom: true},
	} {
		b.Run(mode.name, func(b *testing.B) {
			for _, size := range []int{100, 500, 2000} {
				b.Run(fmt.Sprintf("entities=%d", size), func(b *testing.B) {
					dir, old, project := benchmarkDiffData(b, size, mode.onDisk, mode.custom)
					for b.Loop() {
						diff := ComputeDiff(old, project, dir)
						if diff == nil || len(diff.TypeChanges) != size || len(diff.FieldRenames) != size {
							b.Fatalf("unexpected diff: %d type changes, %d field renames", len(diff.TypeChanges), len(diff.FieldRenames))
						}
					}
				})
			}
		})
	}
}
