package seed

import (
	"math"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

func TestGenerateBasicTypes(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Enums: map[string][]string{"Status": {"pending", "active"}},
		Entities: []ir.Entity{
			{
				Name:       "Product",
				NamePlural: "Products",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "name", DatabaseType: "string"},
					{Name: "price", DatabaseType: "decimal"},
					{Name: "active", DatabaseType: "boolean"},
					{Name: "status", DatabaseType: "Status"},
					{Name: "tags", DatabaseType: "array(string)"},
					{Name: "payload", DatabaseType: "jsonb"},
				},
			},
		},
	}

	ds := Generate(project, Options{CountPerEntity: 3})
	if len(ds.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(ds.Entities))
	}
	em := ds.Entities[0]
	if len(em.Records) != 3 {
		t.Fatalf("got %d records, want 3", len(em.Records))
	}

	first := em.Records[0].Fields
	vals := map[string]any{}
	for _, sv := range first {
		vals[sv.Field] = sv.Value
	}
	if _, ok := vals["id"].(string); !ok {
		t.Errorf("id should be a string uuid, got %T", vals["id"])
	}
	if _, ok := vals["name"].(string); !ok {
		t.Errorf("name should be a string, got %T", vals["name"])
	}
	if _, ok := vals["price"].(float64); !ok {
		t.Errorf("price should be a float64, got %T", vals["price"])
	}
	if vals["active"] != true {
		t.Errorf("active = %v, want true", vals["active"])
	}
	if vals["status"] != "pending" {
		t.Errorf("status = %v, want pending (first enum value)", vals["status"])
	}
	if arr, ok := vals["tags"].([]any); !ok || len(arr) != 1 {
		t.Errorf("tags should be a 1-element slice, got %#v", vals["tags"])
	}
	if _, ok := vals["payload"].(string); !ok {
		t.Errorf("payload should be a JSON string, got %T", vals["payload"])
	}
}

func TestGenerateHonorsArrayDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		dbType     string
		defaultVal string
		want       []any
	}{
		{name: "string elements", dbType: "array(string)", defaultVal: "[alpha, beta]", want: []any{"alpha", "beta"}},
		{name: "int elements", dbType: "array(int)", defaultVal: "[1, 2]", want: []any{int64(1), int64(2)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			project := &ir.Project{
				Entities: []ir.Entity{
					{
						Name: "Tagged",
						Fields: []ir.Field{
							{Name: "id", DatabaseType: "uuid", IsPrimary: true},
							{Name: "tags", DatabaseType: tt.dbType, DefaultValue: tt.defaultVal},
						},
					},
				},
			}
			ds := Generate(project, Options{CountPerEntity: 1})
			got := ds.Entities[0].Records[0].Fields[1].Value
			arr, ok := got.([]any)
			if !ok {
				t.Fatalf("tags = %#v, want []any", got)
			}
			if len(arr) != len(tt.want) {
				t.Fatalf("tags = %#v, want %#v", arr, tt.want)
			}
			for i := range tt.want {
				if arr[i] != tt.want[i] {
					t.Fatalf("tags[%d] = %#v, want %#v (full value %#v)", i, arr[i], tt.want[i], arr)
				}
			}
		})
	}
}

func TestGenerateReferencesParentPK(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name:       "Category",
				NamePlural: "Categories",
				Fields:     []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
			},
			{
				Name:       "Product",
				NamePlural: "Products",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "categoryId", DatabaseType: "uuid", IsRelation: true, RelationTarget: "Category"},
					{Name: "images", DatabaseType: "uuid", IsRelation: true, IsMany: true, RelationTarget: "Image"},
				},
			},
		},
	}

	ds := Generate(project, Options{CountPerEntity: 1})
	var categoryPK any
	for _, sv := range ds.Entities[0].Records[0].Fields {
		if sv.Field == "id" {
			categoryPK = sv.Value
		}
	}
	for _, sv := range ds.Entities[1].Records[0].Fields {
		switch sv.Field {
		case "categoryId":
			if sv.Value != categoryPK {
				t.Errorf("categoryId = %v, want parent PK %v", sv.Value, categoryPK)
			}
		case "images":
			if sv.Value != nil {
				t.Errorf("[many] relation should be nil, got %v", sv.Value)
			}
		}
	}
}

func TestNormalizeCoercesTypes(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name: "Doc",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "title", DatabaseType: "string"},
					{Name: "count", DatabaseType: "int"},
					{Name: "ratio", DatabaseType: "decimal"},
					{Name: "enabled", DatabaseType: "boolean"},
					{Name: "tags", DatabaseType: "array(string)"},
				},
				Seed: []map[string]any{
					{
						"id":      "550e8400-e29b-41d4-a716-446655440000",
						"title":   "hello",
						"count":   7,
						"ratio":   2.5,
						"enabled": true,
						"tags":    []any{"a", "b"},
					},
				},
			},
		},
	}

	ds := Normalize(project)
	if len(ds.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(ds.Entities))
	}
	vals := map[string]any{}
	for _, sv := range ds.Entities[0].Records[0].Fields {
		vals[sv.Field] = sv.Value
	}
	if vals["count"] != int64(7) {
		t.Errorf("count = %#v, want int64(7)", vals["count"])
	}
	if vals["ratio"] != float64(2.5) {
		t.Errorf("ratio = %#v, want float64(2.5)", vals["ratio"])
	}
	if vals["enabled"] != true {
		t.Errorf("enabled = %#v, want true", vals["enabled"])
	}
	if arr, ok := vals["tags"].([]any); !ok || len(arr) != 2 || arr[0] != "a" {
		t.Errorf("tags = %#v, want [a b]", vals["tags"])
	}
}

func TestNormalizeSkipsEntitiesWithoutSeed(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{Name: "Empty", Fields: []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}}},
			{Name: "Seeded", Fields: []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}}, Seed: []map[string]any{{"id": "x"}}},
		},
	}
	ds := Normalize(project)
	if len(ds.Entities) != 1 || ds.Entities[0].Name != "Seeded" {
		t.Fatalf("got %+v, want only Seeded", ds.Entities)
	}
}

func TestNormalizeIncludesRelationFlags(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name: "Order",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "customerId", DatabaseType: "uuid", IsRelation: true, RelationTarget: "Customer"},
				},
				Seed: []map[string]any{{"customerId": "550e8400-e29b-41d4-a716-446655440000"}},
			},
		},
	}
	ds := Normalize(project)
	sv := ds.Entities[0].Records[0].Fields[0]
	if !sv.IsRelation || sv.IsMany {
		t.Errorf("customerId should be a single FK relation, got IsRelation=%v IsMany=%v", sv.IsRelation, sv.IsMany)
	}
	if !strings.HasPrefix(sv.Value.(string), "550e8400") {
		t.Errorf("uuid value = %v, want the declared uuid string", sv.Value)
	}
}

func TestGenerateHonorsNumericBounds(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name: "Bounds",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "qtyInclusiveMin", DatabaseType: "int", Validations: []ir.Validation{{Name: "gte", Value: "10"}}},
					{Name: "qtyExclusiveMin", DatabaseType: "int", Validations: []ir.Validation{{Name: "gt", Value: "5"}}},
					{Name: "qtyExclusiveMax", DatabaseType: "bigint", Validations: []ir.Validation{{Name: "lt", Value: "3"}}},
					{Name: "qtyClampedByExclusiveMax", DatabaseType: "int", Validations: []ir.Validation{{Name: "lt", Value: "1"}}},
					{Name: "priceInclusiveMax", DatabaseType: "decimal", Validations: []ir.Validation{{Name: "lte", Value: "0.25"}}},
				},
			},
		},
	}

	ds := Generate(project, Options{CountPerEntity: 2})
	vals := func(record int) map[string]any {
		m := map[string]any{}
		for _, sv := range ds.Entities[0].Records[record].Fields {
			m[sv.Field] = sv.Value
		}
		return m
	}

	r0, r1 := vals(0), vals(1)
	// The base mock value is record+1; bounds clamp it when violated, so both
	// records may legitimately land on the same clamped number.
	if r0["qtyInclusiveMin"] != int64(10) || r1["qtyInclusiveMin"] != int64(10) {
		t.Errorf("inclusive min: %v / %v, want 10 / 10 (clamped up to the bound)", r0["qtyInclusiveMin"], r1["qtyInclusiveMin"])
	}
	if r0["qtyExclusiveMin"] != int64(6) {
		t.Errorf("exclusive min = %v, want 6 (just above the bound)", r0["qtyExclusiveMin"])
	}
	if r0["qtyExclusiveMax"] != int64(1) || r1["qtyExclusiveMax"] != int64(2) {
		t.Errorf("exclusive max: %v / %v, want 1 / 2 (base values already within the bound)", r0["qtyExclusiveMax"], r1["qtyExclusiveMax"])
	}
	if r0["qtyClampedByExclusiveMax"] != int64(0) {
		t.Errorf("clamped exclusive max = %v, want 0 (just below the violated bound 1)", r0["qtyClampedByExclusiveMax"])
	}
	if r0["priceInclusiveMax"] != float64(0.25) {
		t.Errorf("inclusive max = %v, want 0.25 (clamped down)", r0["priceInclusiveMax"])
	}
}

// TestGenerateRespectsExclusiveBounds pins the mock value at the bound for
// inclusive rules and one unit past it for exclusive ones, across every
// numeric database type. Int/bigint start at record+1 (1 here), float/decimal
// at record+1+0.5 (1.5 here), so a bound equal to the base exercises the
// exclusive branch while an inclusive bound keeps the base unchanged.
func TestGenerateRespectsExclusiveBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		dbType      string
		validations []ir.Validation
		want        any
	}{
		{name: "int exclusive min steps above bound", dbType: "int", validations: []ir.Validation{{Name: "gt", Value: "5"}}, want: int64(6)},
		{name: "bigint exclusive min steps above bound", dbType: "bigint", validations: []ir.Validation{{Name: "gt", Value: "5"}}, want: int64(6)},
		{name: "int exclusive max steps below bound", dbType: "int", validations: []ir.Validation{{Name: "lt", Value: "1"}}, want: int64(0)},
		{name: "bigint exclusive max steps below bound", dbType: "bigint", validations: []ir.Validation{{Name: "lt", Value: "1"}}, want: int64(0)},
		{name: "int inclusive min holds bound", dbType: "int", validations: []ir.Validation{{Name: "gte", Value: "1"}}, want: int64(1)},
		{name: "bigint inclusive max holds bound", dbType: "bigint", validations: []ir.Validation{{Name: "lte", Value: "1"}}, want: int64(1)},
		{name: "float exclusive min steps above bound", dbType: "float", validations: []ir.Validation{{Name: "gt", Value: "1.5"}}, want: float64(2.5)},
		{name: "decimal exclusive min steps above bound", dbType: "decimal", validations: []ir.Validation{{Name: "gt", Value: "1.5"}}, want: float64(2.5)},
		{name: "float exclusive max steps below bound", dbType: "float", validations: []ir.Validation{{Name: "lt", Value: "1.5"}}, want: float64(0.5)},
		{name: "decimal exclusive max steps below bound", dbType: "decimal", validations: []ir.Validation{{Name: "lt", Value: "1.5"}}, want: float64(0.5)},
		{name: "float inclusive min holds bound", dbType: "float", validations: []ir.Validation{{Name: "gte", Value: "1.5"}}, want: float64(1.5)},
		{name: "decimal inclusive max holds bound", dbType: "decimal", validations: []ir.Validation{{Name: "lte", Value: "1.5"}}, want: float64(1.5)},
		{name: "int exclusive min at MaxInt64 saturates", dbType: "int", validations: []ir.Validation{{Name: "gt", Value: "9223372036854775807"}}, want: int64(math.MaxInt64)},
		{name: "bigint exclusive min at MaxInt64 saturates", dbType: "bigint", validations: []ir.Validation{{Name: "gt", Value: "9223372036854775807"}}, want: int64(math.MaxInt64)},
		{name: "int exclusive max at MinInt64 saturates", dbType: "int", validations: []ir.Validation{{Name: "lt", Value: "-9223372036854775808"}}, want: int64(math.MinInt64)},
		{name: "bigint exclusive max at MinInt64 saturates", dbType: "bigint", validations: []ir.Validation{{Name: "lt", Value: "-9223372036854775808"}}, want: int64(math.MinInt64)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := &ir.Project{
				Entities: []ir.Entity{
					{
						Name: "Bounds",
						Fields: []ir.Field{
							{Name: "id", DatabaseType: "uuid", IsPrimary: true},
							{Name: "amount", DatabaseType: tt.dbType, Validations: tt.validations},
						},
					},
				},
			}
			ds := Generate(project, Options{CountPerEntity: 1})
			got := ds.Entities[0].Records[0].Fields[1].Value
			if got != tt.want {
				t.Errorf("amount = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestGenerateHonorsStringValidations(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name: "Strings",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "email", DatabaseType: "string", Validations: []ir.Validation{{Name: "email"}}},
					{Name: "homepage", DatabaseType: "string", Validations: []ir.Validation{{Name: "url"}}},
					{Name: "padded", DatabaseType: "string", Validations: []ir.Validation{{Name: "min", Value: "30"}}},
					{Name: "short", DatabaseType: "string", Validations: []ir.Validation{{Name: "max", Value: "4"}}},
				},
			},
		},
	}

	ds := Generate(project, Options{CountPerEntity: 1})
	vals := map[string]any{}
	for _, sv := range ds.Entities[0].Records[0].Fields {
		vals[sv.Field] = sv.Value
	}
	if vals["email"] != "mock1@example.com" {
		t.Errorf("email = %v", vals["email"])
	}
	if vals["homepage"] != "https://example.com/resource1" {
		t.Errorf("url = %v", vals["homepage"])
	}
	if s, _ := vals["padded"].(string); len(s) < 30 {
		t.Errorf("min_length padding: got %q (%d chars), want at least 30", s, len(s))
	}
	if s, _ := vals["short"].(string); len(s) > 4 {
		t.Errorf("max_length clamp: got %q (%d chars), want at most 4", s, len(s))
	}
}

func TestNormalizeCoerceEdgeCases(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Enums: map[string][]string{"Status": {"draft", "live"}},
		Entities: []ir.Entity{
			{
				Name: "Edges",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "count", DatabaseType: "int"},
					{Name: "big", DatabaseType: "bigint"},
					{Name: "ratio", DatabaseType: "float"},
					{Name: "enabled", DatabaseType: "boolean"},
					{Name: "tags", DatabaseType: "array(string)"},
					{Name: "statuses", DatabaseType: "array(Status)"},
					{Name: "meta", DatabaseType: "jsonb"},
					{Name: "status", DatabaseType: "Status"},
				},
				Seed: []map[string]any{
					{
						"id":       "550e8400-e29b-41d4-a716-446655440000",
						"count":    "not-a-number",
						"big":      float64(7.9),
						"ratio":    "also-not-a-number",
						"enabled":  "not-a-bool",
						"tags":     "oops-not-a-list",
						"statuses": []any{"draft", "live"},
						"meta":     map[string]any{"k": "v"},
						"status":   "draft",
					},
				},
			},
		},
	}

	ds := Normalize(project)
	vals := map[string]any{}
	for _, sv := range ds.Entities[0].Records[0].Fields {
		vals[sv.Field] = sv.Value
	}
	if vals["count"] != int64(0) {
		t.Errorf("unparseable int = %#v, want the zero value int64(0)", vals["count"])
	}
	if vals["big"] != int64(7) {
		t.Errorf("float for bigint = %#v, want truncated int64(7)", vals["big"])
	}
	if vals["ratio"] != float64(0) {
		t.Errorf("unparseable float = %#v, want the zero value float64(0)", vals["ratio"])
	}
	if vals["enabled"] != false {
		t.Errorf("unparseable bool = %#v, want false", vals["enabled"])
	}
	if arr, ok := vals["tags"].([]any); !ok || len(arr) != 0 {
		t.Errorf("non-list for an array field = %#v, want an empty slice", vals["tags"])
	}
	arr, ok := vals["statuses"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "draft" {
		t.Errorf("enum array = %#v, want [draft live]", vals["statuses"])
	}
	if m, ok := vals["meta"].(map[string]any); !ok || m["k"] != "v" {
		t.Errorf("jsonb should keep its native shape, got %#v", vals["meta"])
	}
	if vals["status"] != "draft" {
		t.Errorf("enum scalar = %#v, want the declared wire value draft", vals["status"])
	}
}

func TestGenerateClampsNonPositiveMaxLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		max  string
		want string
	}{
		{name: "zero", max: "0", want: ""},
		{name: "negative", max: "-1", want: ""},
		{name: "oversized", max: "1000", want: "mock_name_1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := &ir.Project{
				Entities: []ir.Entity{
					{
						Name: "Item",
						Fields: []ir.Field{
							{Name: "id", DatabaseType: "uuid", IsPrimary: true},
							{Name: "name", DatabaseType: "string", Validations: []ir.Validation{{Name: "max", Value: tt.max}}},
						},
					},
				},
			}

			ds := Generate(project, Options{CountPerEntity: 1})
			got := ds.Entities[0].Records[0].Fields[1].Value
			if got != tt.want {
				t.Errorf("name = %v, want %q", got, tt.want)
			}
		})
	}
}

// TestStringValueMaxLengthClamp pins the defensive max_length clamp on a
// hand-built field: an oversized max leaves the base string untouched, a zero
// (or negative) max empties it, and a smaller max truncates to that length.
func TestStringValueMaxLengthClamp(t *testing.T) {
	t.Parallel()
	const base = "mock_name_1" // len(base) == 11
	tests := []struct {
		name string
		max  string
		want string
	}{
		{name: "max above length is a no-op", max: "1000", want: base},
		{name: "max equal to length is a no-op", max: "11", want: base},
		{name: "max zero is empty", max: "0", want: ""},
		{name: "max negative is empty", max: "-1", want: ""},
		{name: "max below length truncates", max: "4", want: "mock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := ir.Field{
				Name:         "name",
				DatabaseType: "string",
				Validations:  []ir.Validation{{Name: "max", Value: tt.max}},
			}
			got := stringValue(field, 0)
			if got != tt.want {
				t.Errorf("stringValue(max=%q) = %q, want %q", tt.max, got, tt.want)
			}
		})
	}
}

func TestGenerateRotatesRelationPKs(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name:   "Category",
				Fields: []ir.Field{{Name: "id", DatabaseType: "uuid", IsPrimary: true}},
			},
			{
				Name: "Product",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "categoryId", DatabaseType: "uuid", IsRelation: true, IsUnique: true, RelationTarget: "Category"},
				},
			},
		},
	}

	ds := Generate(project, Options{CountPerEntity: 3})
	parentPKs := make([]any, 0, 3)
	for _, rec := range ds.Entities[0].Records {
		parentPKs = append(parentPKs, rec.Fields[0].Value)
	}
	for i, rec := range ds.Entities[1].Records {
		got := rec.Fields[1].Value
		want := parentPKs[i%len(parentPKs)]
		if got != want {
			t.Errorf("record %d FK = %v, want %v", i, got, want)
		}
	}
	if ds.Entities[1].Records[0].Fields[1].Value == ds.Entities[1].Records[1].Fields[1].Value {
		t.Fatal("two children point at the same parent PK; unique FK rotation failed")
	}
}

func TestRelationValueRotatesParentPKs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		field  ir.Field
		pks    []any
		record int
		want   any
	}{
		{name: "first parent", field: ir.Field{RelationTarget: "Category"}, pks: []any{"a", "b", "c"}, record: 0, want: "a"},
		{name: "second parent", field: ir.Field{RelationTarget: "Category"}, pks: []any{"a", "b", "c"}, record: 1, want: "b"},
		{name: "wraps around", field: ir.Field{RelationTarget: "Category"}, pks: []any{"a", "b", "c"}, record: 3, want: "a"},
		{name: "single parent", field: ir.Field{RelationTarget: "Category"}, pks: []any{"a"}, record: 5, want: "a"},
		{name: "no parents", field: ir.Field{RelationTarget: "Category"}, pks: nil, record: 2},
		{name: "many relation", field: ir.Field{RelationTarget: "Category", IsMany: true}, pks: []any{"a", "b"}, record: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relationValue(tt.field, map[string][]any{"Category": tt.pks}, tt.record)
			if got != tt.want {
				t.Errorf("relationValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateUsesDeclaredDefaultOverMock(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name: "Defaults",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "country", DatabaseType: "string", DefaultValue: "DE"},
					{Name: "attempts", DatabaseType: "int", DefaultValue: "3"},
				},
			},
		},
	}

	ds := Generate(project, Options{CountPerEntity: 1})
	vals := map[string]any{}
	for _, sv := range ds.Entities[0].Records[0].Fields {
		vals[sv.Field] = sv.Value
	}
	if vals["country"] != "DE" {
		t.Errorf("country = %v, want the declared default DE", vals["country"])
	}
	if vals["attempts"] != int64(3) {
		t.Errorf("attempts = %#v, want int64(3)", vals["attempts"])
	}
}

// TestNormalizeIntegralFloatSeed documents that a float with an integral
// value survives seed coercion; fractional values are rejected upstream by
// the validator before they reach this code.
func TestNormalizeIntegralFloatSeed(t *testing.T) {
	t.Parallel()
	project := &ir.Project{
		Entities: []ir.Entity{
			{
				Name: "Docs",
				Fields: []ir.Field{
					{Name: "id", DatabaseType: "uuid", IsPrimary: true},
					{Name: "count", DatabaseType: "int"},
				},
				Seed: []map[string]any{{
					"id":    "550e8400-e29b-41d4-a716-446655440000",
					"count": float64(1.0),
				}},
			},
		},
	}

	ds := Normalize(project)
	vals := map[string]any{}
	for _, sv := range ds.Entities[0].Records[0].Fields {
		vals[sv.Field] = sv.Value
	}
	if vals["count"] != int64(1) {
		t.Errorf("count = %#v, want int64(1)", vals["count"])
	}
}
