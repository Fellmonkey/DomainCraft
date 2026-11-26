package snapshot

// ComputeMigrationPlan compares the previous snapshot (old model) against the
// current IR (new model) and returns an ordered, deterministic migration tree:
// CreateTable / DropTable / RenameTable / AddColumn / DropColumn / RenameColumn /
// AlterColumn. The plan is language-agnostic — bridges translate each op into
// their own migration syntax (EF Core, Alembic, a NoSQL transform, ...).
//
// Unlike ComputeDiff (which is about orphaned files and developer-owned custom
// code), this is about the database schema. Renames use the `old_name` hints;
// a RenameTable/RenameColumn preserves data, whereas a Drop+Add would not.

import (
	"maps"
	"slices"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

// ComputeMigrationPlan builds the abstract migration tree.
func ComputeMigrationPlan(old *Snapshot, project *ir.Project) *ir.MigrationPlan {
	plan := &ir.MigrationPlan{}
	if old == nil || project == nil {
		return plan
	}

	oldEntities := old.Entities
	idx := newEntityIndex(project)
	newEntityNames := slices.Sorted(maps.Keys(idx.byName))

	// --- table-level operations -------------------------------------------

	// CreateTable: entity is new (not in the old model and not a rename).
	for _, name := range newEntityNames {
		e := idx.byName[name]
		if _, existed := oldEntities[name]; existed {
			continue
		}
		if e.OldName != "" {
			if _, wasRenamed := oldEntities[e.OldName]; wasRenamed {
				continue // handled as a rename below
			}
		}
		op := ir.MigrationOp{Kind: ir.OpCreateTable, Entity: e.Name, Table: e.TableName()}
		for _, f := range e.Fields {
			op.Columns = append(op.Columns, migrationColumn(f))
		}
		plan.Operations = append(plan.Operations, op)
	}

	// RenameTable: entity declares old_name pointing at a previously existing entity.
	renamedOldNames := make([]string, 0, len(idx.byOldName))
	for oldName := range idx.byOldName {
		if _, existed := oldEntities[oldName]; existed {
			renamedOldNames = append(renamedOldNames, oldName)
		}
	}
	slices.Sort(renamedOldNames)
	for _, oldName := range renamedOldNames {
		e := idx.byName[idx.byOldName[oldName]]
		plan.Operations = append(plan.Operations, ir.MigrationOp{
			Kind:     ir.OpRenameTable,
			Entity:   e.Name,
			OldName:  oldName,
			Table:    e.TableName(),
			OldTable: ir.TableNameFor(oldName),
		})
	}

	// DropTable: entity existed before but is gone now (and not renamed).
	oldEntityNames := slices.Sorted(maps.Keys(oldEntities))
	for _, name := range oldEntityNames {
		if _, stillExists := idx.byName[name]; stillExists {
			continue
		}
		if _, renamed := idx.byOldName[name]; renamed {
			continue
		}
		plan.Operations = append(plan.Operations, ir.MigrationOp{
			Kind:   ir.OpDropTable,
			Entity: name,
			Table:  ir.TableNameFor(name),
		})
	}

	// --- column-level operations -----------------------------------------
	// For entities that survive (same name, or renamed), diff their fields.
	for _, name := range newEntityNames {
		e := idx.byName[name]
		// The old state this entity corresponds to (rename resolution).
		oldName := name
		if e.OldName != "" {
			if _, ok := oldEntities[e.OldName]; ok {
				oldName = e.OldName
			}
		}
		oldState, ok := oldEntities[oldName]
		if !ok {
			continue // brand-new entity already handled by CreateTable
		}
		diffColumns(plan, oldState, &e)
	}

	return plan
}

// migrationColumn is the single projection from an IR field to a column
// descriptor, shared by CreateTable and AddColumn so new field attributes
// cannot be added to one path and forgotten in the other.
func migrationColumn(f ir.Field) ir.MigrationColumn {
	return ir.MigrationColumn{
		Name:           f.Name,
		Column:         f.ColumnName(),
		DBType:         f.DatabaseType,
		Nullable:       f.IsNullable,
		IsPrimary:      f.IsPrimary,
		IsRelation:     f.IsRelation,
		RelationTarget: f.RelationTarget,
	}
}

// diffColumns appends AddColumn / DropColumn / RenameColumn / AlterColumn ops
// for one surviving entity.
// fieldPointersByName is the migration plan's field index; the diff uses the
// type-only fieldsByName helper, so both agree on field names by construction.
func fieldPointersByName(e *ir.Entity) map[string]*ir.Field {
	fields := make(map[string]*ir.Field, len(e.Fields))
	for i := range e.Fields {
		f := &e.Fields[i]
		fields[f.Name] = f
	}
	return fields
}

func diffColumns(plan *ir.MigrationPlan, oldState EntityState, e *ir.Entity) {
	oldFields := oldState.Fields // field name -> IR database type
	newFieldByName := fieldPointersByName(e)

	table := e.TableName()

	// A rename is only clean when the old field existed AND the new name was
	// not already present in the previous model. Deciding this once here keeps
	// RenameColumn, AddColumn and DropColumn in agreement — previously
	// renameTarget omitted the shadow check and suppressed a needed DropColumn.
	renamedFromOld := make(map[string]*ir.Field)
	for i := range e.Fields {
		f := &e.Fields[i]
		if f.OldName == "" {
			continue
		}
		if _, existed := oldFields[f.OldName]; !existed {
			continue
		}
		if _, shadowed := oldFields[f.Name]; shadowed {
			continue
		}
		renamedFromOld[f.OldName] = f
	}
	oldNames := slices.Sorted(maps.Keys(renamedFromOld))
	for _, oldName := range oldNames {
		f := renamedFromOld[oldName]
		// Prefer the column recorded in the snapshot: it is the canonical name
		// (FK-aware, original name in a chained rename). Fall back for snapshots
		// written before Columns existed.
		oldColumn := oldState.Columns[oldName]
		if oldColumn == "" {
			oldColumn = f.OldColumnName()
		}
		newColumn := f.ColumnName()
		plan.Operations = append(plan.Operations, ir.MigrationOp{
			Kind:      ir.OpRenameColumn,
			Entity:    e.Name,
			Table:     table,
			Column:    newColumn,
			OldColumn: oldColumn,
			DBType:    f.DatabaseType,
			OldDBType: oldFields[oldName],
		})
		// A rename that also changed type is two logical operations for most
		// migration systems (RenameColumn then AlterColumn). Emit both so a bridge
		// never has to re-derive the type change.
		if oldType := oldFields[f.OldName]; oldType != f.DatabaseType {
			plan.Operations = append(plan.Operations, ir.MigrationOp{
				Kind:      ir.OpAlterColumn,
				Entity:    e.Name,
				Table:     table,
				Column:    newColumn,
				DBType:    f.DatabaseType,
				OldDBType: oldType,
			})
		}
	}

	// AddColumn: field present now, absent before, and not a rename.
	for _, f := range e.Fields {
		if _, existed := oldFields[f.Name]; existed {
			continue
		}
		if _, wasRenamed := renamedFromOld[f.OldName]; wasRenamed {
			continue // handled by RenameColumn above
		}
		column := f.ColumnName()
		plan.Operations = append(plan.Operations, ir.MigrationOp{
			Kind:    ir.OpAddColumn,
			Entity:  e.Name,
			Table:   table,
			Column:  column,
			DBType:  f.DatabaseType,
			Columns: []ir.MigrationColumn{migrationColumn(f)},
		})
	}

	// DropColumn: field existed before but is gone now (and not renamed).
	oldFieldNames := slices.Sorted(maps.Keys(oldFields))
	for _, fn := range oldFieldNames {
		if _, stillExists := newFieldByName[fn]; stillExists {
			continue
		}
		if _, wasRenamed := renamedFromOld[fn]; wasRenamed {
			continue
		}
		column := oldState.Columns[fn]
		if column == "" {
			column = textutil.ToDatabaseColumnName(fn)
		}
		plan.Operations = append(plan.Operations, ir.MigrationOp{
			Kind:   ir.OpDropColumn,
			Entity: e.Name,
			Table:  table,
			Column: column,
		})
	}

	// AlterColumn: same field, different database type.
	for _, fn := range oldFieldNames {
		f, ok := newFieldByName[fn]
		if !ok {
			continue
		}
		if oldType := oldFields[fn]; oldType != f.DatabaseType {
			column := f.ColumnName()
			plan.Operations = append(plan.Operations, ir.MigrationOp{
				Kind:      ir.OpAlterColumn,
				Entity:    e.Name,
				Table:     table,
				Column:    column,
				DBType:    f.DatabaseType,
				OldDBType: oldType,
			})
		}
	}
}
