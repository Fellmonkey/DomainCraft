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
	kinds := map[ir.MigrationOpKind]ir.MigrationOp{}
	for _, op := range plan.Operations {
		kinds[op.Kind] = op
	}

	create, ok := kinds[ir.OpCreateTable]
	if !ok || create.Table != "orders" {
		t.Errorf("expected CreateTable(orders), got %+v", kinds[ir.OpCreateTable])
	}
	if len(create.Columns) != 1 || create.Columns[0].Column != "id" {
		t.Errorf("CreateTable should carry columns, got %+v", create.Columns)
	}

	drop, ok := kinds[ir.OpDropTable]
	if !ok || drop.Table != "legacies" {
		t.Errorf("expected DropTable(legacies), got %+v", kinds[ir.OpDropTable])
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
