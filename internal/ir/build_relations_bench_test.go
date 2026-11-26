package ir

import (
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/lexer"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/testutil"
)

var updateGolden = flag.Bool("update", false, "regenerate benchmark golden files")

func checkBenchGolden(t *testing.T, name, got string) {
	t.Helper()
	testutil.CheckGolden(t, name, got, *updateGolden)
}

func benchHash(lines []string) string { return testutil.HashLines(lines) }

// benchmarkSortFixture builds the dependency shapes shared by
// BenchmarkSortEntities and TestBenchmarkSortEntitiesGolden:
//   - chain: entity i depends on i-1 (every pop scans all entities today);
//   - fanout: one root unlocks every other entity in ascending name order;
//   - fanout-reverse: the same root unlocks them in descending name order,
//     the case where out-of-order insertions shifted the whole ready slice.
func benchmarkSortFixture(kind string, size int) []Entity {
	entities := make([]Entity, size)
	for i := range entities {
		name := i
		if kind == "fanout-reverse" {
			name = size - 1 - i
		}
		entities[i] = Entity{Name: fmt.Sprintf("E%04d", name)}
	}
	fieldName := "root"
	if kind == "chain" {
		fieldName = "parent"
	}
	for i := 1; i < size; i++ {
		target := &entities[0]
		if kind == "chain" {
			target = &entities[i-1]
		}
		entities[i].RelationsOut = []Relation{{
			FieldName:    fieldName,
			TargetEntity: target,
			IsMany:       false,
		}}
	}
	return entities
}

func BenchmarkSortEntities(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("chain/%d", size), func(b *testing.B) {
			entities := benchmarkSortFixture("chain", size)
			for b.Loop() {
				_ = sortEntities(entities)
			}
		})
		// Fan-out: one pop makes every other entity ready at once, so the
		// ready queue grows by O(n) insertions at a single step.
		b.Run(fmt.Sprintf("fanout/%d", size), func(b *testing.B) {
			entities := benchmarkSortFixture("fanout", size)
			for b.Loop() {
				_ = sortEntities(entities)
			}
		})
		// Reverse fan-out: the root unlocks every other entity in descending
		// name order, so each binary insert shifts the whole ready slice — the
		// quadratic case the min-heap removes.
		b.Run(fmt.Sprintf("fanout-reverse/%d", size), func(b *testing.B) {
			entities := benchmarkSortFixture("fanout-reverse", size)
			for b.Loop() {
				_ = sortEntities(entities)
			}
		})
	}
}

// TestBenchmarkSortEntitiesGolden pins the order produced by sortEntities and
// the pointer rebinding that goes with it: every relation must point into the
// returned slice, not into the original backing array.
func TestBenchmarkSortEntitiesGolden(t *testing.T) {
	lines := make([]string, 0, 9)
	for _, size := range []int{100, 500, 2000} {
		for _, kind := range []string{"chain", "fanout", "fanout-reverse"} {
			sorted := sortEntities(benchmarkSortFixture(kind, size))
			if len(sorted) != size {
				t.Fatalf("%s/%d: got %d entities, want %d", kind, size, len(sorted), size)
			}
			position := make(map[string]int, len(sorted))
			for i := range sorted {
				position[sorted[i].Name] = i
			}
			var entries []string
			for i := range sorted {
				e := &sorted[i]
				if position[e.Name] != i {
					t.Errorf("%s/%d: %s at index %d, index map says %d", kind, size, e.Name, i, position[e.Name])
				}
				rels := make([]string, 0, len(e.RelationsOut))
				for _, rel := range e.RelationsOut {
					target := "<nil>"
					if rel.TargetEntity != nil {
						idx, ok := position[rel.TargetEntity.Name]
						if !ok || &sorted[idx] != rel.TargetEntity {
							t.Errorf("%s/%d: %s relation points outside the sorted slice", kind, size, e.Name)
						}
						target = rel.TargetEntity.Name
					}
					rels = append(rels, fmt.Sprintf("%s->%s", rel.FieldName, target))
				}
				slices.Sort(rels)
				entries = append(entries, fmt.Sprintf("%s|%s", e.Name, strings.Join(rels, ",")))
			}
			lines = append(lines, fmt.Sprintf("%s/%d\tentities=%d\tsha256=%s", kind, size, len(sorted), benchHash(entries)))
		}
	}
	checkBenchGolden(t, "sort-entities.golden.txt", strings.Join(lines, "\n"))
}

// benchmarkHubSchema builds one hub with N [many] relations to N leaves and a
// single FK from every leaf back to the hub. This exercises the three relation
// reconciliation passes against a single hot target entity.
func benchmarkHubSchema(n int) *parser.ParsedSchema {
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Hub"},
		Entities:    make(map[string]*parser.ParsedEntity, n+1),
		EntityOrder: make([]string, 0, n+1),
	}
	hub := &parser.ParsedEntity{
		Name:       "Hub",
		NamePlural: "Hubs",
		Fields:     make(map[string]*parser.ParsedField, n+1),
		FieldOrder: make([]string, 0, n+1),
	}
	hub.Fields["id"] = &parser.ParsedField{FieldDefinition: &lexer.FieldDefinition{Name: "id", Type: "uuid", IsPrimary: true}}
	hub.FieldOrder = append(hub.FieldOrder, "id")
	for i := range n {
		leaf := fmt.Sprintf("Leaf%04d", i)
		hub.Fields[leaf] = &parser.ParsedField{FieldDefinition: &lexer.FieldDefinition{
			Name: leaf, Type: "relation", TargetEntity: leaf, IsMany: true,
		}}
		hub.FieldOrder = append(hub.FieldOrder, leaf)
		schema.Entities[leaf] = &parser.ParsedEntity{
			Name:       leaf,
			NamePlural: leaf + "s",
			Fields: map[string]*parser.ParsedField{
				"id": {FieldDefinition: &lexer.FieldDefinition{Name: "id", Type: "uuid", IsPrimary: true}},
				"hub": {FieldDefinition: &lexer.FieldDefinition{
					Name: "hub", Type: "relation", TargetEntity: "Hub",
				}},
			},
			FieldOrder: []string{"id", "hub"},
		}
		schema.EntityOrder = append(schema.EntityOrder, leaf)
	}
	schema.Entities["Hub"] = hub
	schema.EntityOrder = append([]string{"Hub"}, schema.EntityOrder...)
	return schema
}

func BenchmarkBuildHubRelations(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("entities=%d", size), func(b *testing.B) {
			schema := benchmarkHubSchema(size)
			for b.Loop() {
				if _, err := Build(schema); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// hubIRDigest serializes the built IR into canonical sorted lines. Fields,
// relation types, pair hints and target names are all covered by the digest;
// map iteration is limited to slices.Sorted(maps.Keys(...)).
func hubIRDigest(t *testing.T, project *Project) string {
	t.Helper()
	byName := make(map[string]*Entity, len(project.Entities))
	for i := range project.Entities {
		byName[project.Entities[i].Name] = &project.Entities[i]
	}
	var lines []string
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		e := byName[name]
		fields := make([]string, 0, len(e.Fields))
		for _, f := range e.Fields {
			fields = append(fields, fmt.Sprintf("%s:%s:pk=%t:relation=%t", f.Name, f.DatabaseType, f.IsPrimary, f.IsRelation))
		}
		slices.Sort(fields)
		lines = append(lines, fmt.Sprintf("entity\t%s\tfields=[%s]", name, strings.Join(fields, ",")))
		for _, side := range []struct {
			label string
			rels  []Relation
		}{
			{label: "out", rels: e.RelationsOut},
			{label: "in", rels: e.RelationsIn},
		} {
			rels := make([]string, 0, len(side.rels))
			for _, rel := range side.rels {
				target := "<nil>"
				if rel.TargetEntity != nil {
					if _, ok := byName[rel.TargetEntity.Name]; !ok {
						t.Errorf("%s %s relation target %q is not in the project", name, side.label, rel.TargetEntity.Name)
					}
					target = rel.TargetEntity.Name
				}
				rels = append(rels, fmt.Sprintf("%s->%s:type=%s:pair=%s:inverse=%s:many=%t:nav=%s",
					rel.FieldName, target, rel.RelationType, rel.PairFieldName, rel.InverseNavName, rel.IsMany, rel.NavigationName))
			}
			slices.Sort(rels)
			lines = append(lines, fmt.Sprintf("%s\t%s\trels=[%s]", side.label, name, strings.Join(rels, ",")))
		}
	}
	slices.Sort(lines)
	return benchHash(lines)
}

// TestBenchmarkBuildHubRelationsGolden pins the relation reconciliation on the
// same hub fixture the benchmark builds: one [many] field per leaf paired with
// the leaf's FK, without an extra inverse collection.
func TestBenchmarkBuildHubRelationsGolden(t *testing.T) {
	lines := make([]string, 0, 3)
	for _, size := range []int{100, 500, 2000} {
		project, err := Build(benchmarkHubSchema(size))
		if err != nil {
			t.Fatalf("Build(size=%d): %v", size, err)
		}
		lines = append(lines, fmt.Sprintf("entities=%d\tir=%d\tsha256=%s", size, len(project.Entities), hubIRDigest(t, project)))
	}
	checkBenchGolden(t, "build-hub-relations.golden.txt", strings.Join(lines, "\n"))
}

// TestBuildHubRelationsPreservesFirstMatch guards the hub case while the
// per-target linear scans are replaced with indexed lookups. Each leaf's FK is
// paired with the hub's [many] field; no duplicate inverse collection is built.
func TestBuildHubRelationsPreservesFirstMatch(t *testing.T) {
	const leaves = 8
	project, err := Build(benchmarkHubSchema(leaves))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	var hub *Entity
	for i := range project.Entities {
		if project.Entities[i].Name == "Hub" {
			hub = &project.Entities[i]
			break
		}
	}
	if hub == nil {
		t.Fatal("Hub entity missing from IR")
	}
	if len(hub.RelationsOut) != leaves { // id is scalar; all other fields are [many]
		t.Fatalf("Hub has %d outgoing relations, want %d", len(hub.RelationsOut), leaves)
	}
	if len(hub.RelationsIn) != 0 {
		t.Errorf("Hub has %d incoming relations, want 0 (paired FKs must not add collections)", len(hub.RelationsIn))
	}
	for i, rel := range hub.RelationsOut {
		if rel.RelationType != "one-to-many" {
			t.Errorf("hub relation %d type = %q, want one-to-many", i, rel.RelationType)
		}
		if rel.TargetEntity == nil {
			t.Errorf("hub relation %d has no target", i)
			continue
		}
		if rel.PairFieldName != "hub" {
			t.Errorf("hub relation %d pair field = %q, want hub", i, rel.PairFieldName)
		}
	}

	for i := range project.Entities {
		e := &project.Entities[i]
		if e.Name == "Hub" {
			continue
		}
		if len(e.RelationsOut) != 1 {
			t.Fatalf("%s has %d outgoing relations, want 1", e.Name, len(e.RelationsOut))
		}
		rel := e.RelationsOut[0]
		if rel.InverseNavName != e.Name {
			t.Errorf("%s inverse nav = %q, want %q", e.Name, rel.InverseNavName, e.Name)
		}
		if rel.IsMany {
			t.Errorf("%s FK relation is many", e.Name)
		}
	}
}
