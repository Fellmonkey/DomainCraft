package snapshot

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

func TestComputeMigrationPlanCreateAndDropTables(t *testing.T) {
	t.Parallel()
	old := &Snapshot{
		Entities: map[string]EntityState{
			"Product": {Fields: map[string]string{"id": "uuid"}},
			"Legacy":  {Fields: map[string]string{"id": "uuid"}},
		},
	}

	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name:       "Product",
				NamePlural: "Products",
				Fields:     []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
			},
			{
				Name:       "Order",
				NamePlural: "Orders",
				Fields:     []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
			},
		},
	}

	plan := ComputeMigrationPlan(old, project)
	// Exact slice: a map keyed by kind would hide duplicate or extra ops.
	if len(plan.Operations) != 2 {
		t.Fatalf("operations = %+v, want CreateTable(orders) then DropTable(legacies)", plan.Operations)
	}
	create, drop := plan.Operations[0], plan.Operations[1]
	if create.Kind != ir.OpCreateTable || create.Table != "orders" {
		t.Errorf("first op = %+v, want CreateTable(orders)", create)
	}
	if len(create.Columns) != 1 || create.Columns[0].Column != "id" {
		t.Errorf("CreateTable should carry columns, got %+v", create.Columns)
	}
	if drop.Kind != ir.OpDropTable || drop.Table != "legacies" {
		t.Errorf("second op = %+v, want DropTable(legacies)", drop)
	}
}

func TestComputeMigrationPlanRenameTable(t *testing.T) {
	t.Parallel()
	old := &Snapshot{
		Entities: map[string]EntityState{
			"Product": {Fields: map[string]string{"id": "uuid"}},
		},
	}

	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name:       "Item",
				OldName:    "Product",
				NamePlural: "Items",
				Fields:     []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
			},
		},
	}

	plan := ComputeMigrationPlan(old, project)
	if len(plan.Operations) != 1 {
		t.Fatalf("got %d ops, want 1: %+v", len(plan.Operations), plan.Operations)
	}
	op := plan.Operations[0]
	if op.Kind != ir.OpRenameTable || op.OldTable != "products" || op.Table != "items" {
		t.Errorf("got %+v, want RenameTable(products -> items)", op)
	}
}

func TestComputeMigrationPlanColumnOps(t *testing.T) {
	t.Parallel()
	old := &Snapshot{
		Entities: map[string]EntityState{
			"Product": {
				Fields: map[string]string{
					"id":    "uuid",
					"title": "string", // renamed -> name
					"price": "int",    // altered -> decimal
					"stock": "int",    // dropped
				},
			},
		},
	}

	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name:       "Product",
				NamePlural: "Products",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{
						Name:                  "name",
						OldName:               "title",
						DatabaseType:          "string",
						DatabaseColumnName:    "name",
						OldDatabaseColumnName: "title",
					},
					{Name: "price", DatabaseType: "decimal", DatabaseColumnName: "price"},
					{Name: "sku", DatabaseType: "string", DatabaseColumnName: "sku"}, // added
				},
			},
		},
	}

	plan := ComputeMigrationPlan(old, project)
	if len(plan.Operations) != 4 {
		t.Fatalf("operations = %+v, want exactly 4 (rename, alter, add, drop)", plan.Operations)
	}
	byKind := map[ir.MigrationOpKind][]ir.MigrationOp{}
	for _, op := range plan.Operations {
		byKind[op.Kind] = append(byKind[op.Kind], op)
	}

	if len(byKind[ir.OpRenameColumn]) != 1 {
		t.Errorf("expected 1 RenameColumn, got %+v", byKind[ir.OpRenameColumn])
	} else {
		op := byKind[ir.OpRenameColumn][0]
		if op.OldColumn != "title" || op.Column != "name" {
			t.Errorf("rename = %s -> %s, want title -> name", op.OldColumn, op.Column)
		}
	}

	if len(byKind[ir.OpAlterColumn]) != 1 {
		t.Errorf("expected 1 AlterColumn, got %+v", byKind[ir.OpAlterColumn])
	} else if op := byKind[ir.OpAlterColumn][0]; op.Column != "price" || op.OldDBType != "int" || op.DBType != "decimal" {
		t.Errorf("alter = %+v, want price int -> decimal", op)
	}

	if len(byKind[ir.OpAddColumn]) != 1 {
		t.Errorf("expected 1 AddColumn, got %+v", byKind[ir.OpAddColumn])
	} else if op := byKind[ir.OpAddColumn][0]; op.Column != "sku" {
		t.Errorf("add = %+v, want column sku", op)
	}

	if len(byKind[ir.OpDropColumn]) != 1 {
		t.Errorf("expected 1 DropColumn, got %+v", byKind[ir.OpDropColumn])
	} else if op := byKind[ir.OpDropColumn][0]; op.Column != "stock" {
		t.Errorf("drop = %+v, want column stock", op)
	}
}

// A relation field's DB column is the FK column (category -> category_id).
// The old snapshot did not keep column names, so DropColumn re-derived the name
// from the logical field and emitted DROP COLUMN category — a no-op that left
// the real FK column behind. New snapshots carry ir.Field.ColumnName().
func TestDropColumnUsesCanonicalRelationColumn(t *testing.T) {
	t.Parallel()

	project := &ir.Project{Entities: []ir.Entity{{
		Name:       "Product",
		NamePlural: "Products",
		Fields: []ir.Field{
			{Name: "id", DatabaseType: "uuid", IsPrimary: true, DatabaseColumnName: "id"},
			{Name: "category", DatabaseType: "uuid", IsRelation: true, DatabaseColumnName: "category_id"},
		},
	}}}
	snap := New("demo", project, nil)
	state := snap.Entities["Product"]
	if got := state.Columns["category"]; got != "category_id" {
		t.Fatalf("snapshot column for relation field = %q, want category_id", got)
	}

	// The relation field is gone from the current model.
	project.Entities[0].Fields = project.Entities[0].Fields[:1]
	plan := ComputeMigrationPlan(snap, project)
	if len(plan.Operations) != 1 {
		t.Fatalf("operations = %+v, want one DropColumn", plan.Operations)
	}
	op := plan.Operations[0]
	if op.Kind != ir.OpDropColumn || op.Column != "category_id" {
		t.Errorf("got %+v, want DropColumn(category_id)", op)
	}
}

// Snapshots written before Columns existed must keep working: the old
// snake_case fallback still applies to them.
func TestDropColumnFallsBackForLegacySnapshot(t *testing.T) {
	t.Parallel()
	old := &Snapshot{Entities: map[string]EntityState{
		"Product": {Fields: map[string]string{"category": "uuid"}},
	}}
	project := &ir.Project{Entities: []ir.Entity{{
		Name:       "Product",
		NamePlural: "Products",
		Fields:     []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
	}}}
	plan := ComputeMigrationPlan(old, project)
	var drop *ir.MigrationOp
	for i := range plan.Operations {
		if plan.Operations[i].Kind == ir.OpDropColumn {
			drop = &plan.Operations[i]
		}
	}
	if drop == nil || drop.Column != "category" {
		t.Errorf("operations = %+v, want DropColumn(category)", plan.Operations)
	}
}

// A rename is ambiguous when the new field name already existed in the old
// model. renamedFields skipped such a rename, but renameTarget (used by the
// DropColumn pass) did not — so the old column was never dropped.
func TestShadowedRenameStillDropsOldColumn(t *testing.T) {
	t.Parallel()
	old := &Snapshot{Entities: map[string]EntityState{
		"Product": {
			Fields:  map[string]string{"id": "uuid", "title": "string", "name": "string"},
			Columns: map[string]string{"id": "id", "title": "title", "name": "name"},
		},
	}}
	project := &ir.Project{Entities: []ir.Entity{{
		Name:       "Product",
		NamePlural: "Products",
		Fields: []ir.Field{
			{Name: "id", DatabaseType: "uuid", IsPrimary: true, DatabaseColumnName: "id"},
			{Name: "name", OldName: "title", DatabaseType: "string", DatabaseColumnName: "name", OldDatabaseColumnName: "title"},
		},
	}}}
	plan := ComputeMigrationPlan(old, project)
	var drop *ir.MigrationOp
	for i := range plan.Operations {
		if plan.Operations[i].Kind == ir.OpDropColumn {
			drop = &plan.Operations[i]
		}
	}
	if drop == nil || drop.Column != "title" {
		t.Errorf("operations = %+v, want DropColumn(title)", plan.Operations)
	}
}

// In a chained rename the physical column may be several names behind the
// declared old_name. Use the column recorded in the snapshot, not a re-derived
// snake_case of old_name, so RenameColumn targets a column that exists.
func TestChainedRenameUsesSnapshotColumn(t *testing.T) {
	t.Parallel()
	old := &Snapshot{Entities: map[string]EntityState{
		"Product": {
			Fields:        map[string]string{"id": "uuid", "title": "string"},
			Columns:       map[string]string{"id": "id", "title": "original_title"},
			FieldOldNames: map[string]string{"title": "headline"},
		},
	}}
	project := &ir.Project{Entities: []ir.Entity{{
		Name:       "Product",
		NamePlural: "Products",
		Fields: []ir.Field{
			{Name: "id", DatabaseType: "uuid", IsPrimary: true, DatabaseColumnName: "id"},
			{Name: "heading", OldName: "title", DatabaseType: "string", DatabaseColumnName: "heading", OldDatabaseColumnName: "title"},
		},
	}}}
	plan := ComputeMigrationPlan(old, project)
	var rename *ir.MigrationOp
	for i := range plan.Operations {
		if plan.Operations[i].Kind == ir.OpRenameColumn {
			rename = &plan.Operations[i]
		}
	}
	if rename == nil || rename.OldColumn != "original_title" || rename.Column != "heading" {
		t.Errorf("operations = %+v, want RenameColumn(original_title -> heading)", plan.Operations)
	}
}

// The rename report is developer-facing and must name the same physical
// columns as the migration plan. For a relation field that means FK columns.
func TestFieldRenameReportUsesCanonicalColumns(t *testing.T) {
	t.Parallel()
	old := &Snapshot{Entities: map[string]EntityState{
		"Product": {
			Fields:  map[string]string{"id": "uuid", "category": "uuid"},
			Columns: map[string]string{"id": "id", "category": "category_id"},
		},
	}}
	project := &ir.Project{Entities: []ir.Entity{{
		Name:       "Product",
		NamePlural: "Products",
		Fields: []ir.Field{
			{Name: "id", DatabaseType: "uuid", IsPrimary: true, DatabaseColumnName: "id"},
			{Name: "cat", OldName: "category", DatabaseType: "uuid", IsRelation: true, DatabaseColumnName: "category_id"},
		},
	}}}
	diff := ComputeDiff(old, project, t.TempDir())
	if len(diff.FieldRenames) != 1 {
		t.Fatalf("field renames = %+v, want 1", diff.FieldRenames)
	}
	fr := diff.FieldRenames[0]
	if fr.OldColumn != "category_id" || fr.NewColumn != "cat_id" {
		t.Errorf("columns = %s -> %s, want category_id -> cat_id", fr.OldColumn, fr.NewColumn)
	}
}

// The migration plan used to derive table names with its own
// ToDatabaseColumnName(Pluralize(...)) helper. It must agree with the IR's
// TableName()/TableNameFor on acronym names, where a hand-rolled snake_case
// diverges (IPv4Address -> i_pv4addresses, not i_pv4_addresses).
func TestMigrationPlanTableNameMatchesIRForAcronymEntity(t *testing.T) {
	t.Parallel()
	const name = "IPv4Address"
	const oldName = "IPv6Address"

	entity := ir.Entity{
		Name:       name,
		NamePlural: "IPv4Addresses",
		Fields:     []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
	}
	if got := entity.TableName(); got != "i_pv4addresses" {
		t.Fatalf("Entity.TableName() = %q, want i_pv4addresses", got)
	}
	if got := ir.TableNameFor(name); got != entity.TableName() {
		t.Errorf("TableNameFor(%q) = %q, want %q", name, got, entity.TableName())
	}

	t.Run("rename table", func(t *testing.T) {
		old := &Snapshot{Entities: map[string]EntityState{oldName: {Fields: map[string]string{"id": "uuid"}}}}
		renamed := entity
		renamed.OldName = oldName
		plan := ComputeMigrationPlan(old, &ir.Project{Entities: []ir.Entity{renamed}})
		if len(plan.Operations) != 1 || plan.Operations[0].Kind != ir.OpRenameTable {
			t.Fatalf("operations = %+v, want one RenameTable", plan.Operations)
		}
		op := plan.Operations[0]
		if op.Table != renamed.TableName() || op.OldTable != ir.TableNameFor(oldName) {
			t.Errorf("rename = %s -> %s, want %s -> %s", op.OldTable, op.Table, ir.TableNameFor(oldName), renamed.TableName())
		}
		if op.OldTable != "i_pv6addresses" {
			t.Errorf("OldTable = %q, want i_pv6addresses", op.OldTable)
		}
	})

	t.Run("drop table", func(t *testing.T) {
		old := &Snapshot{Entities: map[string]EntityState{name: {Fields: map[string]string{"id": "uuid"}}}}
		plan := ComputeMigrationPlan(old, &ir.Project{})
		if len(plan.Operations) != 1 || plan.Operations[0].Kind != ir.OpDropTable {
			t.Fatalf("operations = %+v, want one DropTable", plan.Operations)
		}
		if got := plan.Operations[0].Table; got != ir.TableNameFor(name) {
			t.Errorf("DropTable table = %q, want %q", got, ir.TableNameFor(name))
		}
	})
}

func TestComputeMigrationPlanNilInputs(t *testing.T) {
	t.Parallel()
	if plan := ComputeMigrationPlan(nil, nil); !plan.IsEmpty() {
		t.Error("nil inputs should produce an empty plan")
	}
	if plan := ComputeMigrationPlan(&Snapshot{}, &ir.Project{}); !plan.IsEmpty() {
		t.Error("empty inputs should produce an empty plan")
	}
}

func TestComputeMigrationPlanRenameWithTypeChange(t *testing.T) {
	t.Parallel()
	// A field renamed AND retyped must emit RenameColumn + AlterColumn, not Drop+Add.
	old := &Snapshot{
		Entities: map[string]EntityState{
			"Product": {Fields: map[string]string{"id": "uuid", "title": "string"}},
		},
	}
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name:       "Product",
				NamePlural: "Products",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{
						Name:                  "name",
						OldName:               "title",
						DatabaseType:          "text",
						DatabaseColumnName:    "name",
						OldDatabaseColumnName: "title",
					},
				},
			},
		},
	}

	plan := ComputeMigrationPlan(old, project)
	var renamed, altered bool
	for _, op := range plan.Operations {
		switch op.Kind {
		case ir.OpRenameColumn:
			renamed = true
			if op.OldColumn != "title" || op.Column != "name" {
				t.Errorf("rename = %s -> %s, want title -> name", op.OldColumn, op.Column)
			}
		case ir.OpAlterColumn:
			altered = true
			if op.OldDBType != "string" || op.DBType != "text" {
				t.Errorf("alter = %+v, want string -> text", op)
			}
		}
	}
	if !renamed || !altered {
		t.Errorf("want RenameColumn + AlterColumn, got %+v", plan.Operations)
	}
}
