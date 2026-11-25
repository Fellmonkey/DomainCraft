package ir

import (
	"fmt"
	"slices"
	"testing"
)

// TestTopoSortInsertReadyKeepsNameOrder pins the ready-queue ordering:
// entities that become ready out of name order must still be processed in name
// order (ties broken deterministically by name, not by edge order).
func TestTopoSortInsertReadyKeepsNameOrder(t *testing.T) {
	t.Parallel()
	names := []string{"C", "B", "A", "M", "Z"}
	entities := make([]Entity, len(names))
	index := make(map[string]int, len(names))
	for i, name := range names {
		entities[i] = Entity{Name: name}
		index[name] = i
	}
	addDep := func(from, to string) {
		e := &entities[index[from]]
		e.RelationsOut = append(e.RelationsOut, Relation{
			FieldName:    to,
			TargetEntity: &entities[index[to]],
			IsMany:       false,
		})
	}
	// Z unlocks B then A (edge order is B, A); B unlocks C. M is free.
	addDep("B", "Z")
	addDep("A", "Z")
	addDep("C", "B")

	got := sortEntities(entities)
	want := []string{"M", "Z", "A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("sortEntities() returned %d entities (%+v), want %d", len(got), got, len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("entity %d = %q, want %q (full order: %+v)", i, got[i].Name, name, entityNames(got))
		}
	}
	// Rebound relation pointers must still point into the reordered slice.
	for i := range got {
		for _, rel := range got[i].RelationsOut {
			if rel.TargetEntity == nil || rel.TargetEntity.Name != rel.FieldName {
				t.Errorf("%s relation %q points to %v, want an entity in the reordered slice", got[i].Name, rel.FieldName, rel.TargetEntity)
			}
		}
	}
}

// TestTopoSortFanOutEmitsNameOrder pins the fan-out shape: one root unlocks
// every other entity at once, so the ready queue grows via O(n) insertions.
// Leaves are inserted in descending name order; emission must still be
// ascending, i.e. the heap must pop the smallest name on every step.
func TestTopoSortFanOutEmitsNameOrder(t *testing.T) {
	t.Parallel()
	const leaves = 32
	names := []string{"Root"}
	for i := leaves - 1; i >= 0; i-- {
		names = append(names, fmt.Sprintf("E%04d", i))
	}
	entities := make([]Entity, len(names))
	for i, name := range names {
		entities[i] = Entity{Name: name}
	}
	for i := 1; i < len(entities); i++ {
		entities[i].RelationsOut = []Relation{{
			FieldName:    "root",
			TargetEntity: &entities[0],
			IsMany:       false,
		}}
	}

	want := []string{"Root"}
	for i := range leaves {
		want = append(want, fmt.Sprintf("E%04d", i))
	}
	got := entityNames(sortEntities(entities))
	if !slices.Equal(got, want) {
		t.Fatalf("sortEntities() order = %v, want %v", got, want)
	}
}

func entityNames(entities []Entity) []string {
	names := make([]string, 0, len(entities))
	for _, e := range entities {
		names = append(names, e.Name)
	}
	return names
}
