package ir

import "slices"

// sortEntities reorders the slice by FK dependencies using Kahn's algorithm.
// The order is deterministic: ties are broken by entity name.
// It returns a new slice because relations hold *Entity pointers into the
// original backing array; the original must stay intact until every pointer has
// been rebound to its new location.
func sortEntities(entities []Entity) []Entity {
	byName := make(map[string]int, len(entities))
	for i, e := range entities {
		byName[e.Name] = i
	}

	// deps[i] = set of entity names that entities[i] depends on (must come first).
	// dependents[name] is the inverse edge: indices that must wait for name.
	// Building it once avoids rescanning every entity for every popped name.
	inDegree := make([]int, len(entities))
	dependents := make(map[string][]int, len(entities))
	for i, entity := range entities {
		depSet := make(map[string]bool)
		for _, rel := range entity.RelationsOut {
			if !rel.IsMany && rel.TargetEntity != nil && rel.TargetEntity.Name != entity.Name {
				target := rel.TargetEntity.Name
				if !depSet[target] {
					depSet[target] = true
					dependents[target] = append(dependents[target], i)
				}
			}
		}
		inDegree[i] = len(depSet)
	}

	// Initialize the queue with all entities that have no dependencies.
	ready := make([]string, 0)
	for i, e := range entities {
		if inDegree[i] == 0 {
			ready = append(ready, e.Name)
		}
	}
	// Deterministic ordering: process ready entities in name order.
	slices.Sort(ready)
	insertReady := func(name string) {
		at, _ := slices.BinarySearch(ready, name)
		ready = slices.Insert(ready, at, name)
	}

	order := make([]string, 0, len(entities))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)

		// Decrement only the entities that actually depend on this name.
		// The binary insert keeps the queue sorted, so the emitted order is
		// unchanged from sorting the whole queue after every insertion.
		for _, i := range dependents[name] {
			inDegree[i]--
			if inDegree[i] == 0 {
				insertReady(entities[i].Name)
			}
		}
	}

	// Entities that form a cycle are appended in name order (after all others).
	if len(order) < len(entities) {
		inOrder := make(map[string]bool, len(order))
		for _, name := range order {
			inOrder[name] = true
		}
		remaining := make([]string, 0, len(entities)-len(order))
		for _, e := range entities {
			if !inOrder[e.Name] {
				remaining = append(remaining, e.Name)
			}
		}
		slices.Sort(remaining)
		order = append(order, remaining...)
	}

	// Build the reordered slice. The source `entities` backing array is left
	// untouched, so every TargetEntity pointer still points to valid memory we
	// can read the name from while rebinding it to the new slice.
	sorted := make([]Entity, 0, len(entities))
	for _, name := range order {
		sorted = append(sorted, entities[byName[name]])
	}

	relocated := make(map[string]*Entity, len(sorted))
	for i := range sorted {
		relocated[sorted[i].Name] = &sorted[i]
	}
	for i := range sorted {
		for j := range sorted[i].RelationsOut {
			if sorted[i].RelationsOut[j].TargetEntity != nil {
				sorted[i].RelationsOut[j].TargetEntity = relocated[sorted[i].RelationsOut[j].TargetEntity.Name]
			}
		}
		for j := range sorted[i].RelationsIn {
			if sorted[i].RelationsIn[j].TargetEntity != nil {
				sorted[i].RelationsIn[j].TargetEntity = relocated[sorted[i].RelationsIn[j].TargetEntity.Name]
			}
		}
	}

	return sorted
}
