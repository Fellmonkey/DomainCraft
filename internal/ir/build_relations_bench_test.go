package ir

import (
	"fmt"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/lexer"
	"github.com/DomainCraft/DomainCraft/internal/parser"
)

// benchmarkChainEntities builds a dependency chain: entity i depends on i-1.
// Every pop scans every entity today, so this is the worst case for sortEntities.
func benchmarkChainEntities(n int) []Entity {
	entities := make([]Entity, n)
	for i := range entities {
		entities[i] = Entity{Name: fmt.Sprintf("E%04d", i)}
	}
	for i := 1; i < n; i++ {
		entities[i].RelationsOut = []Relation{{
			FieldName:    "parent",
			TargetEntity: &entities[i-1],
			IsMany:       false,
		}}
	}
	return entities
}

func BenchmarkSortEntities(b *testing.B) {
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("chain/%d", size), func(b *testing.B) {
			entities := benchmarkChainEntities(size)
			for b.Loop() {
				_ = sortEntities(entities)
			}
		})
		// Fan-out: one pop makes every other entity ready at once, so the
		// ready queue grows by O(n) insertions at a single step.
		b.Run(fmt.Sprintf("fanout/%d", size), func(b *testing.B) {
			entities := make([]Entity, size)
			for i := range entities {
				entities[i] = Entity{Name: fmt.Sprintf("E%04d", i)}
			}
			for i := 1; i < size; i++ {
				entities[i].RelationsOut = []Relation{{
					FieldName:    "root",
					TargetEntity: &entities[0],
					IsMany:       false,
				}}
			}
			for b.Loop() {
				_ = sortEntities(entities)
			}
		})
		// Reverse fan-out: the root unlocks every other entity in descending
		// name order, so each binary insert shifts the whole ready slice — the
		// quadratic case the min-heap removes.
		b.Run(fmt.Sprintf("fanout-reverse/%d", size), func(b *testing.B) {
			entities := make([]Entity, size)
			for i := range entities {
				entities[i] = Entity{Name: fmt.Sprintf("E%04d", size-1-i)}
			}
			for i := 1; i < size; i++ {
				entities[i].RelationsOut = []Relation{{
					FieldName:    "root",
					TargetEntity: &entities[0],
					IsMany:       false,
				}}
			}
			for b.Loop() {
				_ = sortEntities(entities)
			}
		})
	}
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
