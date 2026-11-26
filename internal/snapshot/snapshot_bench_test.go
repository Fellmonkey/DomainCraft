package snapshot

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/testutil"
)

var updateGolden = flag.Bool("update", false, "regenerate benchmark golden files")

// benchmarkDiffData builds an old snapshot with five manifest files per entity
// and a current project where every entity has both a type change and a field
// rename hint, so ComputeDiff visits customEntityFiles on both passes.
func benchmarkDiffData(tb testing.TB, entities int, onDisk, custom bool) (string, *Snapshot, *ir.Project) {
	tb.Helper()
	dir := tb.TempDir()
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
					tb.Fatal(err)
				}
				if err := os.WriteFile(abs, []byte("generated\n"), 0o644); err != nil {
					tb.Fatal(err)
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

// benchmarkDiffModes is shared by the benchmark and the golden test so a new
// mode cannot be measured without being pinned.
var benchmarkDiffModes = []struct {
	name   string
	onDisk bool
	custom bool
}{
	{name: "no-custom", onDisk: false, custom: false},
	{name: "manifest", onDisk: false, custom: true},
	{name: "on-disk", onDisk: true, custom: true},
}

func BenchmarkComputeDiff(b *testing.B) {
	for _, mode := range benchmarkDiffModes {
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

// diffDigest serializes every diff field into canonical sorted lines. The
// slices produced by ComputeDiff are sorted again here so the digest never
// depends on map iteration order inside the core.
func diffDigest(t *testing.T, diff *Diff) string {
	t.Helper()
	fileRefs := func(refs []FileRef) string {
		paths := make([]string, 0, len(refs))
		for _, ref := range refs {
			paths = append(paths, fmt.Sprintf("%s:custom=%t", ref.Path, ref.Custom))
		}
		slices.Sort(paths)
		return strings.Join(paths, ",")
	}
	customFiles := func(paths []string) string {
		sorted := slices.Clone(paths)
		slices.Sort(sorted)
		return strings.Join(sorted, ",")
	}
	var lines []string
	for _, d := range diff.Deleted {
		lines = append(lines, fmt.Sprintf("deleted\t%s\tfiles=[%s]", d.Name, fileRefs(d.Files)))
	}
	for _, r := range diff.Renamed {
		lines = append(lines, fmt.Sprintf("renamed\t%s->%s\tfiles=[%s]", r.OldName, r.NewName, fileRefs(r.Files)))
	}
	for _, c := range diff.TypeChanges {
		lines = append(lines, fmt.Sprintf("typechange\t%s.%s\t%s->%s\tcustom=[%s]", c.Entity, c.Field, c.OldType, c.NewType, customFiles(c.CustomFiles)))
	}
	for _, r := range diff.FieldRenames {
		lines = append(lines, fmt.Sprintf("fieldrename\t%s\t%s->%s\t%s->%s\t%s->%s\tchained=%t\tcustom=[%s]",
			r.Entity, r.OldField, r.NewField, r.OldColumn, r.NewColumn, r.OldType, r.NewType, r.Chained, customFiles(r.CustomFiles)))
	}
	if n := diff.NamespaceRename; n != nil {
		lines = append(lines, fmt.Sprintf("namespace\t%s->%s\tfiles=[%s]", n.OldNamespace, n.NewNamespace, customFiles(n.Files)))
	}
	slices.Sort(lines)
	return testutil.HashLines(lines)
}

// TestBenchmarkComputeDiffGolden pins the diff result for every benchmark
// mode, using the same fixture builder as BenchmarkComputeDiff. It uses small
// entity counts on purpose: the golden checks behavior, not speed. Running the
// benchmark's 100/500/2000 sizes here added ~157 s to every `go test` run —
// those sizes belong in BenchmarkComputeDiff and in scripts/bench-check.sh.
func TestBenchmarkComputeDiffGolden(t *testing.T) {
	lines := make([]string, 0, len(benchmarkDiffModes)*2)
	for _, mode := range benchmarkDiffModes {
		for _, size := range []int{10, 50} {
			dir, old, project := benchmarkDiffData(t, size, mode.onDisk, mode.custom)
			diff := ComputeDiff(old, project, dir)
			if diff == nil {
				t.Fatalf("%s/%d: ComputeDiff returned nil", mode.name, size)
			}
			lines = append(lines, fmt.Sprintf("%s/%d\tdeleted=%d\trenamed=%d\ttypechanges=%d\tfieldrenames=%d\tnamespace=%t\tsha256=%s",
				mode.name, size, len(diff.Deleted), len(diff.Renamed), len(diff.TypeChanges), len(diff.FieldRenames), diff.NamespaceRename != nil, diffDigest(t, diff)))
		}
	}
	testutil.CheckGolden(t, "compute-diff.golden.txt", strings.Join(lines, "\n"), *updateGolden)
}
