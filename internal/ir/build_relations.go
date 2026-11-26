package ir

import (
	"fmt"

	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

// buildForwardRelations (Pass 2a) builds forward (outgoing) relations for every
// entity. It must run as a separate pass from the inverse pass below so the
// inverse logic always sees the complete set of RelationsOut regardless of
// entity declaration order.
func buildForwardRelations(entities []Entity, schema *parser.ParsedSchema, entityIndex map[string]*Entity) error {
	for i := range entities {
		irEntity := &entities[i]
		sourceEntity := schema.Entities[irEntity.Name]
		if sourceEntity == nil {
			continue
		}

		for _, fieldName := range sourceEntity.FieldOrder {
			field := sourceEntity.Fields[fieldName]
			if field == nil || !field.IsRelation() {
				continue
			}

			targetEntity, ok := entityIndex[field.TargetEntity]
			if !ok {
				return fmt.Errorf("relation target '%s' referenced by '%s.%s' does not exist", field.TargetEntity, irEntity.Name, field.Name)
			}

			irEntity.RelationsOut = append(irEntity.RelationsOut, Relation{
				FieldName:        field.Name,
				TargetEntity:     targetEntity,
				NavigationName:   field.NavigationName(),
				InverseNavName:   textutil.Pluralize(irEntity.Name),
				OnDeleteBehavior: field.OnDelete,
				IsNullable:       field.IsOptional,
				IsMany:           field.IsMany,
				RelationType:     field.RelationType,
			})
		}
	}
	return nil
}

// relationIndex groups an entity's forward relations by the name of the entity
// they point at. Each bucket keeps the FIRST relation in RelationsOut order,
// matching the early return / break of the linear scans it replaces.
type relationIndex struct {
	singleBack map[string]*Relation
	manyBack   map[string]*Relation
}

// indexRelationsOut builds that view once before the reconciliation passes. A
// hub with N inbound relations used to be rescanned N times per pass.
func indexRelationsOut(entities []Entity) map[string]*relationIndex {
	index := make(map[string]*relationIndex, len(entities))
	for i := range entities {
		owner := &entities[i]
		byTarget := index[owner.Name]
		if byTarget == nil {
			byTarget = &relationIndex{
				singleBack: make(map[string]*Relation),
				manyBack:   make(map[string]*Relation),
			}
			index[owner.Name] = byTarget
		}
		for j := range owner.RelationsOut {
			rel := &owner.RelationsOut[j]
			if rel.TargetEntity == nil {
				continue
			}
			bucket := byTarget.singleBack
			if rel.IsMany {
				bucket = byTarget.manyBack
			}
			if _, seen := bucket[rel.TargetEntity.Name]; !seen {
				bucket[rel.TargetEntity.Name] = rel
			}
		}
	}
	return index
}

// reconcileOneToMany (Pass 2b) detects one-to-many relations declared on BOTH
// sides. When entity A has a `[many]` relation to B AND B declares a single FK
// relation back to A, the two declarations describe the SAME one-to-many
// relationship — not a many-to-many. The FK lives on B; A's [many] is its
// collection navigation. Without this reconciliation a bridge would generate a
// spurious join table in addition to the FK.
func reconcileOneToMany(entities []Entity, index map[string]*relationIndex) {
	for i := range entities {
		entity := &entities[i]
		for j := range entity.RelationsOut {
			rel := &entity.RelationsOut[j]
			if !rel.IsMany || rel.TargetEntity == nil {
				continue
			}
			var pair *Relation
			if bySource := index[rel.TargetEntity.Name]; bySource != nil {
				pair = bySource.singleBack[entity.Name]
			}
			// singleBack holds only !IsMany relations with a non-nil target,
			// bucketed by that target's name — so the lookup above already
			// satisfies the validator's single-back rule. The validator keeps
			// the explicit check because its inputs are user-declared.
			if pair == nil {
				continue
			}
			rel.RelationType = "one-to-many"
			rel.PairFieldName = pair.FieldName
			rel.PairNavigationName = pair.NavigationName
			rel.OnDeleteBehavior = pair.OnDeleteBehavior
			rel.IsNullable = pair.IsNullable
		}
	}
}

// resolveInverseNavs (Pass 2c) resolves each forward relation's inverse-navigation
// name to the actual collection on the target entity. For OrderItem→Order the
// inverse should be "Items" (Order's forward nav), not the computed "OrderItems".
func resolveInverseNavs(entities []Entity, index map[string]*relationIndex) {
	for i := range entities {
		entity := &entities[i]
		for j := range entity.RelationsOut {
			rel := &entity.RelationsOut[j]
			if rel.TargetEntity == nil || rel.IsMany {
				continue
			}
			if bySource := index[rel.TargetEntity.Name]; bySource != nil {
				if back := bySource.manyBack[entity.Name]; back != nil {
					rel.InverseNavName = textutil.PascalCase(back.FieldName)
				}
			}
		}
	}
}

// buildIncomingRelations (Pass 3d) builds inverse (incoming) relations. Each
// incoming relation is a collection navigation on the target that points back
// to the owning entity.
func buildIncomingRelations(entities []Entity, index map[string]*relationIndex) {
	usedInverse := make(map[string]map[string]bool) // target entity -> defined inverse nav names
	for i := range entities {
		entity := &entities[i]
		for j := range entity.RelationsOut {
			rel := &entity.RelationsOut[j]
			target := rel.TargetEntity
			if target == nil {
				continue
			}
			// A double-declared one-to-many: the collection already exists as the
			// forward [many] field — no separate inverse collection on target.
			if rel.IsMany && rel.RelationType == "one-to-many" {
				continue
			}
			// If the target already declares its own forward IsMany relation back
			// to this entity, the inverse collection lives there instead.
			if bySource := index[target.Name]; bySource != nil && bySource.manyBack[entity.Name] != nil {
				continue
			}
			invName := rel.InverseNavName
			if usedInverse[target.Name] != nil && usedInverse[target.Name][invName] {
				// Two relations from the same source to the same target — disambiguate
				// with the field name (e.g. EscrowContract.Buyer / EscrowContract.Seller).
				invName = textutil.PascalCase(rel.FieldName) + textutil.Pluralize(entity.Name)
				for usedInverse[target.Name] != nil && usedInverse[target.Name][invName] {
					invName += "Side"
				}
			}
			if usedInverse[target.Name] == nil {
				usedInverse[target.Name] = make(map[string]bool)
			}
			usedInverse[target.Name][invName] = true
			rel.InverseNavName = invName
			target.RelationsIn = append(target.RelationsIn, Relation{
				FieldName:        rel.FieldName,
				TargetEntity:     entity,
				InverseNavName:   invName,
				OnDeleteBehavior: rel.OnDeleteBehavior,
				IsNullable:       rel.IsNullable,
				IsMany:           !rel.IsMany,
				RelationType:     rel.RelationType,
			})
		}
	}
}
