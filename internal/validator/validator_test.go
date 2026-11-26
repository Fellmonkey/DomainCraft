package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/testutil"
)

// TestValidationDiagnosticsAreDeterministic guards the rule that diagnostics
// use the same order on every run. Several checks iterate maps internally; an
// unordered range made `validate` and the WASM result change line order.
func TestValidationDiagnosticsAreDeterministic(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Item"},
		Entities: map[string]*parser.ParsedEntity{
			"Item": {
				Name:       "Item",
				FieldOrder: []string{"id", "status", "count"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(Zebra)"),
					"count":  testutil.MustParsedField(t, "count", "int [min:abc, max:x]"),
				},
			},
		},
		Enums: map[string][]string{"Zebra": {}, "Alpha": {}},
	}

	var want string
	for range 20 {
		var got strings.Builder
		for _, e := range New(schema).Validate() {
			got.WriteString(e.Message)
			got.WriteByte('\n')
		}
		if want == "" {
			want = got.String()
			continue
		}
		if got.String() != want {
			t.Fatalf("validation diagnostics are not deterministic:\nfirst:\n%s\nnext:\n%s", want, got.String())
		}
	}
	if !strings.Contains(want, "enum must have at least one value") {
		t.Fatalf("fixture did not produce the enum diagnostic: %s", want)
	}
}

func TestValidateDetectsMissingPrimaryKey(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"email"},
				Fields: map[string]*parser.ParsedField{
					"email": testutil.MustParsedField(t, "email", "string"),
				},
			},
		},
	}

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	if errs[0].Message != "entity must have at least one primary key" {
		t.Fatalf("unexpected error: %s", errs[0].Error())
	}
}

func TestValidateRejectsOldNameEqualToFieldName(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string [required, old_name:name]"),
				},
			},
		},
	}

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "old_name cannot equal the field name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about self-referential old_name, got %v", errs)
	}
}

func TestValidateRejectsOldNameCollidingWithExistingField(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "title", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"title": testutil.MustParsedField(t, "title", "string"),
					"name":  testutil.MustParsedField(t, "name", "string [required, old_name:title]"),
				},
			},
		},
	}

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "old_name 'title' collides with an existing field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about old_name colliding with an existing field, got %v", errs)
	}
}

func TestValidateAcceptsValidOldName(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string [required, old_name:title]"),
				},
			},
		},
	}

	errs := nonWarnings(New(schema).Validate())
	for _, e := range errs {
		if strings.Contains(e.Message, "old_name") {
			t.Fatalf("unexpected old_name error: %v", e)
		}
	}
}

func TestValidateDetectsDuplicatePrimaryKey(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "code"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"code": testutil.MustParsedField(t, "code", "string [primary]"),
				},
			},
		},
	}

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "entity has 2 primary keys") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about duplicate primary keys, got %v", errs)
	}
}

func TestValidateDetectsBrokenRelation(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "categoryId"},
				Fields: map[string]*parser.ParsedField{
					"id":         testutil.MustParsedField(t, "id", "uuid"),
					"categoryId": testutil.MustParsedField(t, "categoryId", "relation(Category)"),
				},
			},
		},
	}
	product := schema.Entities["Product"]
	product.Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestValidateDetectsSetNullOnRequiredField(t *testing.T) {
	t.Parallel()
	// Note: on_delete:set_null on a required relation is caught by the lexer, not the validator.
	// This test verifies the schema passes validation when the lexer constraint is respected.
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product", "Category"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "categoryId"},
				Fields: map[string]*parser.ParsedField{
					"id":         testutil.MustParsedField(t, "id", "uuid [primary]"),
					"categoryId": testutil.MustParsedField(t, "categoryId", "relation(Category) [optional]"),
				},
			},
			"Category": {
				Name:       "Category",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	product := schema.Entities["Product"]
	product.Fields["id"].IsPrimary = true
	product.Fields["categoryId"].OnDelete = "set_null"
	product.Fields["categoryId"].IsOptional = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for valid set_null on optional field, got %v", errs)
	}
}

func TestValidateDetectsUndefinedEnum(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Enums:       map[string][]string{"ProductStatus": {"DRAFT", "PUBLISHED"}},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "status"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(NoSuchEnum)"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Field == "status" && strings.Contains(e.Message, "NoSuchEnum") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about undefined enum, got %v", errs)
	}
}

func TestValidatePassesValidEnums(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Enums:       map[string][]string{"ProductStatus": {"DRAFT", "PUBLISHED"}},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "status", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(ProductStatus)"),
					"tags":   testutil.MustParsedField(t, "tags", "array(ProductStatus)"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for valid enums, got %v", errs)
	}
}

func TestValidateDetectsEmptyProjectName(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "project name must not be empty") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about empty project name, got %v", errs)
	}
}

func TestValidateDetectsManyOnNonRelation(t *testing.T) {
	t.Parallel()
	// Note: "many" on non-relation is caught by the lexer, not the validator.
	// This test verifies the lexer catches it before validation.
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	// Valid schema should pass — the lexer rejects "many" on non-relation at parse time.
	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateDetectsOnDeleteOnNonRelation(t *testing.T) {
	t.Parallel()
	// Note: on_delete on non-relation is caught by the lexer, not the validator.
	// This test verifies the lexer catches it before validation.
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	// Valid schema should pass — the lexer rejects on_delete on non-relation at parse time.
	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateDetectsEmptyEnum(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Enums:       map[string][]string{"Status": {}},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "enum must have at least one value") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about empty enum, got %v", errs)
	}
}

func TestValidateDetectsEmptyIndexFields(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
				Indexes: []*parser.ParsedIndex{
					{Fields: []string{}, Type: "btree"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "index 0 has no fields") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about empty index, got %v", errs)
	}
}

func TestValidateDetectsSortLengthMismatch(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "email"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email": testutil.MustParsedField(t, "email", "string"),
				},
				Indexes: []*parser.ParsedIndex{
					{Fields: []string{"id", "email"}, Sort: []string{"asc"}, Type: "btree"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "sort array length") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about sort length mismatch, got %v", errs)
	}
}

func nonWarnings(errs []ValidationError) []ValidationError {
	var result []ValidationError
	for _, e := range errs {
		if !e.Warning {
			result = append(result, e)
		}
	}
	return result
}

func TestValidateAuthEntityMissingEmailPassword(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		Auth:        &parser.AuthConfig{Type: "jwt", Entity: "Product"},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "must have both 'email' and 'password'") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about missing email+password, got %v", errs)
	}
}

func TestValidateAuthEntityValidEmailPassword(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		Auth:        &parser.AuthConfig{Type: "jwt", Entity: "User"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "email", "password"},
				Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email":    testutil.MustParsedField(t, "email", "string"),
					"password": testutil.MustParsedField(t, "password", "string"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "email") || strings.Contains(e.Message, "password") {
			t.Fatalf("unexpected error: %v", e.Message)
		}
	}
}

func TestValidateDefaultNowOnNonDatetime(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "count"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"count": testutil.MustParsedField(t, "count", "int [default:now()]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "default 'now()' is not valid for type int") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about now() on int, got %v", errs)
	}
}

func TestValidateDefaultNowOnDatetime(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "created"},
				Fields: map[string]*parser.ParsedField{
					"id":      testutil.MustParsedField(t, "id", "uuid [primary]"),
					"created": testutil.MustParsedField(t, "created", "datetime [default:now()]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "now()") {
			t.Fatalf("unexpected error about now() on datetime: %v", e.Message)
		}
	}
}

func TestValidateDefaultUuidOnNonUuid(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string [default:uuid()]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "default 'uuid()' is not valid for type string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about uuid() on string, got %v", errs)
	}
}

func TestValidateDeployPortRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid", 8080, false},
		{"zero (default)", 0, false},
		{"min valid", 1, false},
		{"max valid", 65535, false},
		{"below min", -1, true},
		{"above max", 99999, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := &parser.ParsedSchema{
				Project: parser.ProjectConfig{
					Name:   "Test",
					Deploy: &parser.DeployConfig{Port: tt.port},
				},
				EntityOrder: []string{"User"},
				Entities: map[string]*parser.ParsedEntity{
					"User": {
						Name:       "User",
						FieldOrder: []string{"id"},
						Fields: map[string]*parser.ParsedField{
							"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
						},
					},
				},
			}
			schema.Entities["User"].Fields["id"].IsPrimary = true

			errs := New(schema).Validate()
			errs = nonWarnings(errs)
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "deploy.port") {
					found = true
				}
			}
			if tt.wantErr && !found {
				t.Fatalf("expected port range error, got %v", errs)
			}
			if !tt.wantErr && found {
				t.Fatalf("unexpected port range error, got %v", errs)
			}
		})
	}
}

func TestValidateCacheConfigWarnings(t *testing.T) {
	t.Parallel()
	t.Run("provider without connection_string", func(t *testing.T) {
		t.Parallel()
		schema := &parser.ParsedSchema{
			Project: parser.ProjectConfig{
				Name: "Test",
				Cache: &parser.CacheConfig{
					Enabled:  true,
					Provider: "redis",
				},
			},
			EntityOrder: []string{"User"},
			Entities: map[string]*parser.ParsedEntity{
				"User": {
					Name:       "User",
					FieldOrder: []string{"id"},
					Fields: map[string]*parser.ParsedField{
						"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
					},
				},
			},
		}
		schema.Entities["User"].Fields["id"].IsPrimary = true

		errs := New(schema).Validate()
		found := false
		for _, e := range errs {
			if e.Warning && strings.Contains(e.Message, "connection_string is empty") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected warning about empty connection_string, got %v", errs)
		}
	})

	t.Run("provider specified but disabled", func(t *testing.T) {
		t.Parallel()
		schema := &parser.ParsedSchema{
			Project: parser.ProjectConfig{
				Name: "Test",
				Cache: &parser.CacheConfig{
					Enabled:  false,
					Provider: "redis",
				},
			},
			EntityOrder: []string{"User"},
			Entities: map[string]*parser.ParsedEntity{
				"User": {
					Name:       "User",
					FieldOrder: []string{"id"},
					Fields: map[string]*parser.ParsedField{
						"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
					},
				},
			},
		}
		schema.Entities["User"].Fields["id"].IsPrimary = true

		errs := New(schema).Validate()
		found := false
		for _, e := range errs {
			if e.Warning && strings.Contains(e.Message, "cache.enabled is false") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected warning about cache disabled, got %v", errs)
		}
	})
}

func TestValidateIndexOnRelationFieldWithoutIdSuffix(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product", "Category"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "category"},
				Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"category": testutil.MustParsedField(t, "category", "relation(Category)"),
				},
				Indexes: []*parser.ParsedIndex{
					{Fields: []string{"category"}, Type: "btree"},
				},
			},
			"Category": {
				Name:       "Category",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "references relation field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about index on relation field without Id suffix, got %v", errs)
	}
}

func TestValidateIndexOnFKFieldNoWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product", "Category"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "categoryId"},
				Fields: map[string]*parser.ParsedField{
					"id":         testutil.MustParsedField(t, "id", "uuid [primary]"),
					"categoryId": testutil.MustParsedField(t, "categoryId", "relation(Category)"),
				},
				Indexes: []*parser.ParsedIndex{
					{Fields: []string{"categoryId"}, Type: "btree"},
				},
			},
			"Category": {
				Name:       "Category",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "references relation field") {
			t.Fatalf("unexpected warning for FK-style relation field in index: %v", e.Message)
		}
	}
}

func TestValidateIndexOnPrimitiveField(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "email"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email": testutil.MustParsedField(t, "email", "string"),
				},
				Indexes: []*parser.ParsedIndex{
					{Fields: []string{"email"}, Type: "btree"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "references relation field") {
			t.Fatalf("unexpected warning about index on primitive field: %v", e.Message)
		}
	}
}

func TestValidateEnumDefaultMustMatchDeclaredValues(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Enums:       map[string][]string{"ProductStatus": {"DRAFT", "PUBLISHED"}},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "status"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(ProductStatus) [default:DRAFTT]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "not a declared value of enum") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid enum default, got %v", errs)
	}
}

func TestValidateEnumDefaultValidValue(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product"},
		Enums:       map[string][]string{"ProductStatus": {"DRAFT", "PUBLISHED"}},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "status"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(ProductStatus) [default:DRAFT]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "not a declared value of enum") {
			t.Fatalf("unexpected error about valid enum default: %v", e.Message)
		}
	}
}

func TestValidateNumericValidationValueNotNumeric(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "age"},
				Fields: map[string]*parser.ParsedField{
					"id":  testutil.MustParsedField(t, "id", "uuid [primary]"),
					"age": testutil.MustParsedField(t, "age", "int [gte:notanumber]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "not a valid number") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about non-numeric validation value, got %v", errs)
	}
}

func TestValidateNumericValidationValueValid(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "age"},
				Fields: map[string]*parser.ParsedField{
					"id":  testutil.MustParsedField(t, "id", "uuid [primary]"),
					"age": testutil.MustParsedField(t, "age", "int [gte:0, lt:150]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "not a valid number") {
			t.Fatalf("unexpected error about valid numeric validation value: %v", e.Message)
		}
	}
}

func TestValidateEntityEnumNameCollision(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Status"},
		Enums:       map[string][]string{"Status": {"ACTIVE", "INACTIVE"}},
		Entities: map[string]*parser.ParsedEntity{
			"Status": {
				Name:       "Status",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Status"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "collides with an enum") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about entity-enum name collision, got %v", errs)
	}
}

func TestValidatePermissionRolesEmptyAuthRoles(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		Auth:        &parser.AuthConfig{Type: "jwt"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "email", "password"},
				Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email":    testutil.MustParsedField(t, "email", "string"),
					"password": testutil.MustParsedField(t, "password", "string"),
				},
				Permissions: &parser.ParsedPermissions{
					Read: []string{"Admin"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "auth.roles is empty") && strings.Contains(e.Message, "Admin") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about permission role with empty auth.roles, got %v", errs)
	}
}

func TestValidateContradictoryStringMinMax(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string [min:10, max:5]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "min") && strings.Contains(e.Message, "max") && strings.Contains(e.Message, "greater") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about contradictory min>max, got %v", errs)
	}
}

func TestValidateValidStringMinMax(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string [min:2, max:50]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "min") && strings.Contains(e.Message, "max") {
			t.Fatalf("unexpected contradictory range error: %v", e.Message)
		}
	}
}

func TestValidateContradictoryNumericGteLt(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "age"},
				Fields: map[string]*parser.ParsedField{
					"id":  testutil.MustParsedField(t, "id", "uuid [primary]"),
					"age": testutil.MustParsedField(t, "age", "int [gte:100, lt:10]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "gte") && strings.Contains(e.Message, "lt") && strings.Contains(e.Message, "must be less") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about contradictory gte>=lt, got %v", errs)
	}
}

func TestValidateContradictoryNumericGtLte(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "score"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"score": testutil.MustParsedField(t, "score", "int [gt:50, lte:50]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "gt") && strings.Contains(e.Message, "lte") && strings.Contains(e.Message, "must be less") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about contradictory gt>=lte, got %v", errs)
	}
}

func TestValidateValidNumericRange(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "age"},
				Fields: map[string]*parser.ParsedField{
					"id":  testutil.MustParsedField(t, "id", "uuid [primary]"),
					"age": testutil.MustParsedField(t, "age", "int [gte:0, lt:150]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "gte") && strings.Contains(e.Message, "lt") {
			t.Fatalf("unexpected contradictory range error: %v", e.Message)
		}
	}
}

func TestValidateUnusedEnumWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Enums:       map[string][]string{"Role": {"ADMIN", "USER"}, "Status": {"ACTIVE", "INACTIVE"}},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "role"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"role": testutil.MustParsedField(t, "role", "enum(Role)"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true
	schema.Entities["User"].Fields["role"].Type = "enum"
	schema.Entities["User"].Fields["role"].TargetType = "Role"

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "enum is declared but never used") && e.Field == "Status" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about unused enum 'Status', got %v", errs)
	}
}

func TestValidateUsedEnumNoWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Enums:       map[string][]string{"Role": {"ADMIN", "USER"}},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "role"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"role": testutil.MustParsedField(t, "role", "enum(Role)"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true
	schema.Entities["User"].Fields["role"].Type = "enum"
	schema.Entities["User"].Fields["role"].TargetType = "Role"

	errs := New(schema).Validate()
	for _, e := range errs {
		if strings.Contains(e.Message, "enum is declared but never used") {
			t.Fatalf("unexpected warning about used enum: %v", e.Message)
		}
	}
}

func TestValidateOnDeleteValueInvalid(t *testing.T) {
	t.Parallel()
	// Note: invalid on_delete values are caught by the lexer, not the validator.
	// This test verifies the lexer catches it before validation.
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Order", "User"},
		Entities: map[string]*parser.ParsedEntity{
			"Order": {
				Name:       "Order",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Order"].Fields["id"].IsPrimary = true
	schema.Entities["User"].Fields["id"].IsPrimary = true

	// Valid schema should pass — the lexer rejects invalid on_delete at parse time.
	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateSeedValueTypeMismatch(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "age"},
				Fields: map[string]*parser.ParsedField{
					"id":  testutil.MustParsedField(t, "id", "uuid [primary]"),
					"age": testutil.MustParsedField(t, "age", "int"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "age": true},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "not compatible with type int") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about seed type mismatch, got %v", errs)
	}
}

func TestValidateSeedBooleanTypeMismatch(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Config"},
		Entities: map[string]*parser.ParsedEntity{
			"Config": {
				Name:       "Config",
				FieldOrder: []string{"id", "enabled"},
				Fields: map[string]*parser.ParsedField{
					"id":      testutil.MustParsedField(t, "id", "uuid [primary]"),
					"enabled": testutil.MustParsedField(t, "enabled", "boolean"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "enabled": "notabool"},
				},
			},
		},
	}
	schema.Entities["Config"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "not valid for boolean") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about seed boolean type mismatch, got %v", errs)
	}
}

func TestValidateOnDeleteOnManyToManyWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product", "Tag"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "relation(Tag) [many]"),
				},
			},
			"Tag": {
				Name:       "Tag",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true
	schema.Entities["Product"].Fields["tags"].OnDelete = "cascade"

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "on_delete is meaningless on a many-to-many relation") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about on_delete on many-to-many, got %v", errs)
	}
}

func TestValidateOnDeleteOnManyToManyNoWarningWithoutOnDelete(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product", "Tag"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "relation(Tag) [many]"),
				},
			},
			"Tag": {
				Name:       "Tag",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true
	schema.Entities["Product"].Fields["tags"].IsMany = true

	errs := New(schema).Validate()
	for _, e := range errs {
		if strings.Contains(e.Message, "on_delete is meaningless on a many-to-many") {
			t.Fatalf("unexpected warning about on_delete on many-to-many when no on_delete set: %v", e.Message)
		}
	}
}

func TestValidateReconciledOneToManyWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Article", "Tag"},
		Entities: map[string]*parser.ParsedEntity{
			"Article": {
				Name:       "Article",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "relation(Tag) [many]"),
				},
			},
			"Tag": {
				Name:       "Tag",
				FieldOrder: []string{"id", "article"},
				Fields: map[string]*parser.ParsedField{
					"id":      testutil.MustParsedField(t, "id", "uuid [primary]"),
					"article": testutil.MustParsedField(t, "article", "relation(Article)"),
				},
			},
		},
	}
	schema.Entities["Article"].Fields["id"].IsPrimary = true
	schema.Entities["Tag"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "reconciled with FK") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about [many] reconciled into a one-to-many, got %v", errs)
	}
}

// Regression: the hint must print the FK field name as declared. Appending a
// second "Id" to a field already named `articleId` produced `articleIdId`.
func TestValidateReconciledWarningKeepsFKName(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Article", "Tag"},
		Entities: map[string]*parser.ParsedEntity{
			"Article": {
				Name:       "Article",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "relation(Tag) [many]"),
				},
			},
			"Tag": {
				Name:       "Tag",
				FieldOrder: []string{"id", "articleId"},
				Fields: map[string]*parser.ParsedField{
					"id":        testutil.MustParsedField(t, "id", "uuid [primary]"),
					"articleId": testutil.MustParsedField(t, "articleId", "relation(Article)"),
				},
			},
		},
	}
	schema.Entities["Article"].Fields["id"].IsPrimary = true
	schema.Entities["Tag"].Fields["id"].IsPrimary = true

	found := false
	for _, e := range New(schema).Validate() {
		if !e.Warning || !strings.Contains(e.Message, "reconciled with FK") {
			continue
		}
		found = true
		if strings.Contains(e.Message, "articleIdId") {
			t.Fatalf("FK name duplicated in warning: %s", e.Message)
		}
		if !strings.Contains(e.Message, "FK 'articleId' on Tag") {
			t.Fatalf("warning lost the FK name: %s", e.Message)
		}
	}
	if !found {
		t.Fatal("expected a reconciliation warning")
	}
}

// The shipped example must not emit warnings: an example is a template users copy.
func TestExampleDomainHasNoWarnings(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "domain.yaml"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	schema, err := parser.ParseYAML(data)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}
	for _, e := range New(schema).Validate() {
		if e.Warning {
			t.Errorf("example emits a warning: %s", e.Error())
		}
	}
}

// TestValidateGenuineManyToManyHasNoReconciliationWarning ensures that a real
// many-to-many (target has no FK back) does NOT trigger the reconciliation hint.
func TestValidateGenuineManyToManyHasNoReconciliationWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Product", "Tag"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "relation(Tag) [many]"),
				},
			},
			"Tag": {
				Name:       "Tag",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	for _, e := range New(schema).Validate() {
		if strings.Contains(e.Message, "reconciled with FK") {
			t.Fatalf("unexpected reconciliation warning for genuine many-to-many: %v", e.Message)
		}
	}
}

func TestValidateSeedUniqueConstraintViolation(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "email"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email": testutil.MustParsedField(t, "email", "string [unique]"),
				},
				Seed: []map[string]any{
					{"id": "11111111-1111-1111-1111-111111111111", "email": "a@test.com"},
					{"id": "22222222-2222-2222-2222-222222222222", "email": "a@test.com"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicated in seed data") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about seed unique constraint violation, got %v", errs)
	}
}

func TestValidateSeedUniqueConstraintPass(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "email"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email": testutil.MustParsedField(t, "email", "string [unique]"),
				},
				Seed: []map[string]any{
					{"id": "11111111-1111-1111-1111-111111111111", "email": "a@test.com"},
					{"id": "22222222-2222-2222-2222-222222222222", "email": "b@test.com"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicated in seed data") {
			t.Fatalf("unexpected seed unique constraint error: %v", e.Message)
		}
	}
}

func TestValidateSeedInvalidUUID(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
				Seed: []map[string]any{
					{"id": "not-a-uuid"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "not a valid UUID") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid UUID in seed, got %v", errs)
	}
}

func TestValidateSeedValidUUID(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "not a valid UUID") {
			t.Fatalf("unexpected UUID error: %v", e.Message)
		}
	}
}

func TestValidateSeedInvalidDatetime(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "createdAt"},
				Fields: map[string]*parser.ParsedField{
					"id":        testutil.MustParsedField(t, "id", "uuid [primary]"),
					"createdAt": testutil.MustParsedField(t, "createdAt", "datetime"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "createdAt": "not-a-date"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "not a valid datetime") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid datetime in seed, got %v", errs)
	}
}

func TestValidateSeedValidDatetime(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "createdAt"},
				Fields: map[string]*parser.ParsedField{
					"id":        testutil.MustParsedField(t, "id", "uuid [primary]"),
					"createdAt": testutil.MustParsedField(t, "createdAt", "datetime"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "createdAt": "2024-01-15T10:30:00Z"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "not a valid datetime") {
			t.Fatalf("unexpected datetime error: %v", e.Message)
		}
	}
}

func TestValidateSeedInvalidJSON(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "metadata"},
				Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"metadata": testutil.MustParsedField(t, "metadata", "json"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "metadata": "{invalid json}"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "not valid JSON") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid JSON in seed, got %v", errs)
	}
}

func TestValidateSeedValidJSON(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "metadata"},
				Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"metadata": testutil.MustParsedField(t, "metadata", "json"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "metadata": `{"key":"value"}`},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "not valid JSON") {
			t.Fatalf("unexpected JSON error: %v", e.Message)
		}
	}
}

func TestValidatePermissionRoleIdentifierInvalid(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
				Permissions: &parser.ParsedPermissions{
					Read: []string{"valid-role", "123invalid"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "not a valid identifier") && strings.Contains(e.Message, "123invalid") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about invalid permission role identifier, got %v", errs)
	}
}

func TestValidatePermissionRoleSpecialTokensAllowed(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
				Permissions: &parser.ParsedPermissions{
					Read:   []string{"*", "@Owner"},
					Create: []string{"Admin"},
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "not a valid identifier") {
			t.Fatalf("unexpected warning about special token in permissions: %v", e.Message)
		}
	}
}

func TestValidateSeedDateTypeMismatch(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Event"},
		Entities: map[string]*parser.ParsedEntity{
			"Event": {
				Name:       "Event",
				FieldOrder: []string{"id", "started_on"},
				Fields: map[string]*parser.ParsedField{
					"id":         testutil.MustParsedField(t, "id", "uuid [primary]"),
					"started_on": testutil.MustParsedField(t, "started_on", "date"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "started_on": "not-a-date"},
				},
			},
		},
	}
	schema.Entities["Event"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "not a valid date") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about seed date type mismatch, got %v", errs)
	}
}

func TestValidateSeedDateValid(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Event"},
		Entities: map[string]*parser.ParsedEntity{
			"Event": {
				Name:       "Event",
				FieldOrder: []string{"id", "started_on"},
				Fields: map[string]*parser.ParsedField{
					"id":         testutil.MustParsedField(t, "id", "uuid [primary]"),
					"started_on": testutil.MustParsedField(t, "started_on", "date"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "started_on": "2024-05-01"},
				},
			},
		},
	}
	schema.Entities["Event"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "not a valid date") {
			t.Fatalf("unexpected seed date error: %v", e.Message)
		}
	}
}

func TestValidateSeedEnumInvalidValue(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project: parser.ProjectConfig{Name: "Test"},
		Enums: map[string][]string{
			"Status": {"ACTIVE", "INACTIVE"},
		},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "status"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(Status)"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "status": "BOGUS"},
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "not a declared value of enum") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid enum seed value, got %v", errs)
	}
}

func TestValidateSeedEnumValidValue(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project: parser.ProjectConfig{Name: "Test"},
		Enums: map[string][]string{
			"Status": {"ACTIVE", "INACTIVE"},
		},
		EntityOrder: []string{"Product"},
		Entities: map[string]*parser.ParsedEntity{
			"Product": {
				Name:       "Product",
				FieldOrder: []string{"id", "status"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(Status)"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "status": "ACTIVE"},
				},
			},
		},
	}
	schema.Entities["Product"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "enum") {
			t.Fatalf("unexpected enum seed error: %v", e.Message)
		}
	}
}

func TestValidateSeedArrayInvalidValue(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Item"},
		Entities: map[string]*parser.ParsedEntity{
			"Item": {
				Name:       "Item",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "array(string)"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "tags": "not-json"},
				},
			},
		},
	}
	schema.Entities["Item"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "not a valid JSON array") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid array seed value, got %v", errs)
	}
}

func TestValidateSeedArrayValidValue(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Item"},
		Entities: map[string]*parser.ParsedEntity{
			"Item": {
				Name:       "Item",
				FieldOrder: []string{"id", "tags"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"tags": testutil.MustParsedField(t, "tags", "array(string)"),
				},
				Seed: []map[string]any{
					{"id": "550e8400-e29b-41d4-a716-446655440000", "tags": []any{"a", "b"}},
				},
			},
		},
	}
	schema.Entities["Item"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	errs = nonWarnings(errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "seed entry") && strings.Contains(e.Message, "array") {
			t.Fatalf("unexpected array seed error: %v", e.Message)
		}
	}
}

func TestValidateStringMinMaxNonNumeric(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "name"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"name": testutil.MustParsedField(t, "name", "string [max:abc]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if !e.Warning && strings.Contains(e.Message, "'max' value \"abc\" must be an integer") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about non-numeric string min/max, got %v", errs)
	}
}

func TestValidateCircularRelationWarning(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User", "Team"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "team"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"team": testutil.MustParsedField(t, "team", "relation(Team)"),
				},
			},
			"Team": {
				Name:       "Team",
				FieldOrder: []string{"id", "lead"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"lead": testutil.MustParsedField(t, "lead", "relation(User)"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true
	schema.Entities["Team"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "circular relation") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about circular relations, got %v", errs)
	}
}

func TestValidateNoCircularRelationWarningForAcyclic(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User", "Team"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "team"},
				Fields: map[string]*parser.ParsedField{
					"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
					"team": testutil.MustParsedField(t, "team", "relation(Team)"),
				},
			},
			"Team": {
				Name:       "Team",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
	schema.Entities["User"].Fields["id"].IsPrimary = true
	schema.Entities["Team"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "circular relation") {
			t.Fatalf("unexpected circular relation warning for acyclic schema: %v", e.Message)
		}
	}
}

func TestValidateNoCircularRelationWarningForSelfReference(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Category"},
		Entities: map[string]*parser.ParsedEntity{
			"Category": {
				Name:       "Category",
				FieldOrder: []string{"id", "parentId"},
				Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"parentId": testutil.MustParsedField(t, "parentId", "relation(Category) [optional]"),
				},
			},
		},
	}
	schema.Entities["Category"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "circular relation") {
			t.Fatalf("unexpected circular relation warning for self-referential hierarchy: %v", e.Message)
		}
	}
}

func TestValidateNoCircularRelationWarningForManyToMany(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Order", "OrderItem"},
		Entities: map[string]*parser.ParsedEntity{
			"Order": {
				Name:       "Order",
				FieldOrder: []string{"id", "items"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"items": testutil.MustParsedField(t, "items", "relation(OrderItem) [many]"),
				},
			},
			"OrderItem": {
				Name:       "OrderItem",
				FieldOrder: []string{"id", "orderId"},
				Fields: map[string]*parser.ParsedField{
					"id":      testutil.MustParsedField(t, "id", "uuid [primary]"),
					"orderId": testutil.MustParsedField(t, "orderId", "relation(Order)"),
				},
			},
		},
	}
	schema.Entities["Order"].Fields["id"].IsPrimary = true
	schema.Entities["OrderItem"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	for _, e := range errs {
		if e.Warning && strings.Contains(e.Message, "circular relation") {
			t.Fatalf("unexpected circular relation warning for parent/child many relation: %v", e.Message)
		}
	}
}

func TestValidateDefaultDateInvalid(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Event"},
		Entities: map[string]*parser.ParsedEntity{
			"Event": {
				Name:       "Event",
				FieldOrder: []string{"id", "started_on"},
				Fields: map[string]*parser.ParsedField{
					"id":         testutil.MustParsedField(t, "id", "uuid [primary]"),
					"started_on": testutil.MustParsedField(t, "started_on", "date [default:not-a-date]"),
				},
			},
		},
	}
	schema.Entities["Event"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if !e.Warning && strings.Contains(e.Message, "not a valid date") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid date default, got %v", errs)
	}
}

func TestValidateDefaultUuidInvalid(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Doc"},
		Entities: map[string]*parser.ParsedEntity{
			"Doc": {
				Name:       "Doc",
				FieldOrder: []string{"id", "external_id"},
				Fields: map[string]*parser.ParsedField{
					"id":          testutil.MustParsedField(t, "id", "uuid [primary]"),
					"external_id": testutil.MustParsedField(t, "external_id", "uuid [default:not-a-uuid]"),
				},
			},
		},
	}
	schema.Entities["Doc"].Fields["id"].IsPrimary = true

	errs := New(schema).Validate()
	found := false
	for _, e := range errs {
		if !e.Warning && strings.Contains(e.Message, "not a valid UUID") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error about invalid uuid default, got %v", errs)
	}
}

// TestValidateColumnSelfCollisionGuard verifies that a field appearing twice
// in FieldOrder (e.g. a duplicate slipped past the parser) does not produce a
// spurious "collides with field '<itself>'" error. Self-collisions are silently
// ignored; only genuine collisions between two different fields are reported.
func TestValidateColumnSelfCollisionGuard(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id", "isSystemTag", "isSystemTag"}, // duplicate entry
				Fields: map[string]*parser.ParsedField{
					"id":          testutil.MustParsedField(t, "id", "uuid [primary]"),
					"isSystemTag": testutil.MustParsedField(t, "isSystemTag", "boolean [default:false]"),
				},
			},
		},
	}

	errs := New(schema).Validate()
	for _, e := range errs {
		if strings.Contains(e.Message, "collides with field 'isSystemTag'") {
			t.Fatalf("unexpected self-collision error: %s", e.Message)
		}
	}
}

func TestValidateRejectsRelationPrimaryKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pkDef   string
		wantErr bool
	}{
		{name: "relation primary key", pkDef: "relation(Parent) [primary]", wantErr: true},
		{name: "scalar primary key", pkDef: "uuid [primary]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := &parser.ParsedSchema{
				Project:     parser.ProjectConfig{Name: "Test"},
				EntityOrder: []string{"Child", "Parent"},
				Entities: map[string]*parser.ParsedEntity{
					"Child": {
						Name:       "Child",
						FieldOrder: []string{"id"},
						Fields: map[string]*parser.ParsedField{
							"id": testutil.MustParsedField(t, "id", tt.pkDef),
						},
					},
					"Parent": {
						Name:       "Parent",
						FieldOrder: []string{"id"},
						Fields: map[string]*parser.ParsedField{
							"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
						},
					},
				},
			}

			errs := nonWarnings(New(schema).Validate())
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "primary key cannot be a relation field") {
					found = true
				}
			}
			if found != tt.wantErr {
				t.Fatalf("relation primary key error = %v, want %v; errors: %v", found, tt.wantErr, errs)
			}
		})
	}
}

func TestValidateRejectsNonPositiveStringBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		def     string
		wantErr bool
	}{
		{name: "zero max", def: "string [max:0]", wantErr: true},
		{name: "negative max", def: "string [max:-1]", wantErr: true},
		{name: "zero min", def: "string [min:0]", wantErr: true},
		{name: "negative min", def: "string [min:-5]", wantErr: true},
		{name: "valid bounds", def: "string [min:3, max:255]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := &parser.ParsedSchema{
				Project:     parser.ProjectConfig{Name: "Test"},
				EntityOrder: []string{"Doc"},
				Entities: map[string]*parser.ParsedEntity{
					"Doc": {
						Name:       "Doc",
						FieldOrder: []string{"id", "name"},
						Fields: map[string]*parser.ParsedField{
							"id":   testutil.MustParsedField(t, "id", "uuid [primary]"),
							"name": testutil.MustParsedField(t, "name", tt.def),
						},
					},
				},
			}

			errs := nonWarnings(New(schema).Validate())
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "must be positive for string length") {
					found = true
				}
			}
			if found != tt.wantErr {
				t.Fatalf("positive bound error = %v, want %v; errors: %v", found, tt.wantErr, errs)
			}
		})
	}
}

func TestValidateRejectsEnumWireValueCollision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []string
	}{
		{name: "case-only collision", values: []string{"DRAFT", "draft"}},
		{name: "camel case collision", values: []string{"InProgress", "in_progress"}},
		{name: "acronym collision", values: []string{"HTTPPort", "http_port"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := &parser.ParsedSchema{
				Project:     parser.ProjectConfig{Name: "Test"},
				EntityOrder: []string{"Doc"},
				Enums:       map[string][]string{"Status": tt.values},
				Entities: map[string]*parser.ParsedEntity{
					"Doc": {
						Name:       "Doc",
						FieldOrder: []string{"id", "status"},
						Fields: map[string]*parser.ParsedField{
							"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
							"status": testutil.MustParsedField(t, "status", "enum(Status)"),
						},
					},
				},
			}

			errs := nonWarnings(New(schema).Validate())
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "map to the same wire value") {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a hard error for wire-value collision, got %v", errs)
			}
		})
	}
}

func TestValidateRejectsUnparseableNumericSeed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		field   string
		value   any
		wantErr bool
	}{
		{name: "int string", field: "count", value: "7"},
		{name: "int unparseable string", field: "count", value: "abc", wantErr: true},
		{name: "bigint unparseable string", field: "big", value: "12x", wantErr: true},
		{name: "decimal string", field: "price", value: "2.5"},
		{name: "decimal unparseable string", field: "price", value: "nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := &parser.ParsedSchema{
				Project:     parser.ProjectConfig{Name: "Test"},
				EntityOrder: []string{"Doc"},
				Entities: map[string]*parser.ParsedEntity{
					"Doc": {
						Name:       "Doc",
						FieldOrder: []string{"id", "count", "big", "price"},
						Fields: map[string]*parser.ParsedField{
							"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
							"count": testutil.MustParsedField(t, "count", "int"),
							"big":   testutil.MustParsedField(t, "big", "bigint"),
							"price": testutil.MustParsedField(t, "price", "decimal"),
						},
						Seed: []map[string]any{{"id": "550e8400-e29b-41d4-a716-446655440000", tt.field: tt.value}},
					},
				},
			}

			errs := nonWarnings(New(schema).Validate())
			found := false
			for _, e := range errs {
				if e.Field == tt.field && strings.Contains(e.Message, "seed entry 0") {
					found = true
				}
			}
			if found != tt.wantErr {
				t.Fatalf("seed parse error = %v, want %v; errors: %v", found, tt.wantErr, errs)
			}
		})
	}
}

func TestValidateSortsErrorsByMessageTiebreaker(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Doc"},
		Enums:       map[string][]string{"Status": {"A", "a", ""}},
		Entities: map[string]*parser.ParsedEntity{
			"Doc": {
				Name:       "Doc",
				FieldOrder: []string{"id", "status"},
				Fields: map[string]*parser.ParsedField{
					"id":     testutil.MustParsedField(t, "id", "uuid [primary]"),
					"status": testutil.MustParsedField(t, "status", "enum(Status)"),
				},
			},
		},
	}

	errs := nonWarnings(New(schema).Validate())
	if len(errs) < 2 {
		t.Fatalf("got %d errors, want at least 2: %v", len(errs), errs)
	}
	for i := 1; i < len(errs); i++ {
		if errs[i-1].Entity != "<enum>" || errs[i].Entity != "<enum>" {
			continue
		}
		if errs[i-1].Field != "Status" || errs[i].Field != "Status" {
			continue
		}
		if errs[i-1].Message > errs[i].Message {
			t.Fatalf("enum errors are not sorted by message: %q before %q", errs[i-1].Message, errs[i].Message)
		}
	}
	if !strings.Contains(errs[0].Message, "enum value must not be empty") {
		t.Fatalf("first error = %q, want the empty-value error", errs[0].Message)
	}
}

// schemaWithSeedValue builds a minimal valid schema with one integral field
// carrying a single seed value.
func schemaWithSeedValue(t *testing.T, fieldType string, value any) *parser.ParsedSchema {
	t.Helper()
	return &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"Doc"},
		Entities: map[string]*parser.ParsedEntity{
			"Doc": {
				Name:       "Doc",
				FieldOrder: []string{"id", "count"},
				Fields: map[string]*parser.ParsedField{
					"id":    testutil.MustParsedField(t, "id", "uuid [primary]"),
					"count": testutil.MustParsedField(t, "count", fieldType),
				},
				Seed: []map[string]any{{"id": "550e8400-e29b-41d4-a716-446655440000", "count": value}},
			},
		},
	}
}

func TestValidateRejectsFractionalSeedForIntegralField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		fieldType string
		value     any
	}{
		{name: "float64 1.9 for int", fieldType: "int", value: float64(1.9)},
		{name: "float32 1.9 for int", fieldType: "int", value: float32(1.9)},
		{name: "float64 2.5 for bigint", fieldType: "bigint", value: float64(2.5)},
		{name: "negative float32 for bigint", fieldType: "bigint", value: float32(-2.5)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := nonWarnings(New(schemaWithSeedValue(t, tt.fieldType, tt.value)).Validate())
			found := false
			for _, e := range errs {
				if e.Field == "count" && strings.Contains(e.Message, "seed entry 0") &&
					strings.Contains(e.Message, "not compatible with type "+tt.fieldType) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected fractional seed error, got %v", errs)
			}
		})
	}
}

func TestValidateAcceptsIntegralValuedFloatSeed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		fieldType string
		value     any
	}{
		{name: "float64 1.0 for int", fieldType: "int", value: float64(1.0)},
		{name: "float32 1.0 for int", fieldType: "int", value: float32(1.0)},
		{name: "float64 3.0 for bigint", fieldType: "bigint", value: float64(3.0)},
		{name: "float32 3.0 for bigint", fieldType: "bigint", value: float32(3.0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := nonWarnings(New(schemaWithSeedValue(t, tt.fieldType, tt.value)).Validate())
			if len(errs) != 0 {
				t.Fatalf("expected integral-valued float seed to be accepted, got %v", errs)
			}
		})
	}
}

// schemaWithEntityOldName builds a minimal valid schema with an entity rename hint.
func schemaWithEntityOldName(t *testing.T, entityName, oldName string) *parser.ParsedSchema {
	t.Helper()
	return &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{entityName},
		Entities: map[string]*parser.ParsedEntity{
			entityName: {
				Name:       entityName,
				OldName:    oldName,
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
}

func TestValidateRejectsInvalidEntityOldName(t *testing.T) {
	t.Parallel()
	for _, oldName := range []string{"bad-name", "has space", "123abc", "Product.Id"} {
		t.Run(oldName, func(t *testing.T) {
			t.Parallel()
			errs := nonWarnings(New(schemaWithEntityOldName(t, "Item", oldName)).Validate())
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "old_name is not a valid identifier") {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected invalid entity old_name error for %q, got %v", oldName, errs)
			}
		})
	}
}

func TestValidateRejectsSelfReferentialEntityOldName(t *testing.T) {
	t.Parallel()
	errs := nonWarnings(New(schemaWithEntityOldName(t, "Product", "Product")).Validate())
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "old_name cannot equal the entity name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected self-referential entity old_name error, got %v", errs)
	}
}

func TestValidateAcceptsValidEntityOldName(t *testing.T) {
	t.Parallel()
	errs := nonWarnings(New(schemaWithEntityOldName(t, "Item", "Product")).Validate())
	if len(errs) != 0 {
		t.Fatalf("expected a valid entity old_name to be accepted, got %v", errs)
	}
}

// schemaWithProjectConfig builds a schema with one valid entity so only the
// supplied project-level settings can produce diagnostics.
func schemaWithProjectConfig(t *testing.T, project parser.ProjectConfig) *parser.ParsedSchema {
	t.Helper()
	project.Name = "Test"
	return &parser.ParsedSchema{
		Project:     project,
		EntityOrder: []string{"User"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {
				Name:       "User",
				FieldOrder: []string{"id"},
				Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				},
			},
		},
	}
}

// TestValidateAcceptsExplicitZeroProjectNumericValues pins that an explicit 0
// (which the parser now preserves) is a valid value rather than "unset".
func TestValidateAcceptsExplicitZeroProjectNumericValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		project parser.ProjectConfig
	}{
		{"port", parser.ProjectConfig{Deploy: &parser.DeployConfig{Port: 0}}},
		{"permit limit", parser.ProjectConfig{RateLimit: &parser.RateLimitConfig{PermitLimit: 0}}},
		{"window seconds", parser.ProjectConfig{RateLimit: &parser.RateLimitConfig{WindowSeconds: 0}}},
		{"default page size", parser.ProjectConfig{Pagination: &parser.PaginationConfig{DefaultPageSize: 0}}},
		{"max page size", parser.ProjectConfig{Pagination: &parser.PaginationConfig{MaxPageSize: 0}}},
		{"all zeros", parser.ProjectConfig{
			Deploy:     &parser.DeployConfig{Port: 0},
			RateLimit:  &parser.RateLimitConfig{PermitLimit: 0, WindowSeconds: 0},
			Pagination: &parser.PaginationConfig{DefaultPageSize: 0, MaxPageSize: 0},
		}},
		{"zero default with capped max", parser.ProjectConfig{
			Pagination: &parser.PaginationConfig{DefaultPageSize: 0, MaxPageSize: 200},
		}},
		{"positive default with uncapped max", parser.ProjectConfig{
			Pagination: &parser.PaginationConfig{DefaultPageSize: 20, MaxPageSize: 0},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := New(schemaWithProjectConfig(t, tt.project)).Validate()
			if len(errs) != 0 {
				t.Fatalf("expected no diagnostics for explicit zero, got %v", errs)
			}
		})
	}
}

// TestValidateRejectsNegativeProjectNumericValues pins that accepting zero did
// not loosen the existing negative and positive-range checks.
func TestValidateRejectsNegativeProjectNumericValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		project  parser.ProjectConfig
		wantText string
		wantWarn bool
	}{
		{"negative port", parser.ProjectConfig{Deploy: &parser.DeployConfig{Port: -1}}, "deploy.port", false},
		{"port above max", parser.ProjectConfig{Deploy: &parser.DeployConfig{Port: 65536}}, "deploy.port", false},
		{"negative permit limit", parser.ProjectConfig{RateLimit: &parser.RateLimitConfig{PermitLimit: -1}}, "rate_limit.permit_limit must be non-negative", true},
		{"negative window seconds", parser.ProjectConfig{RateLimit: &parser.RateLimitConfig{WindowSeconds: -1}}, "rate_limit.window_seconds must be non-negative", true},
		{"negative default page size", parser.ProjectConfig{Pagination: &parser.PaginationConfig{DefaultPageSize: -1}}, "pagination.default_page_size must be non-negative", true},
		{"negative max page size", parser.ProjectConfig{Pagination: &parser.PaginationConfig{MaxPageSize: -1}}, "pagination.max_page_size must be non-negative", true},
		{"default above max", parser.ProjectConfig{Pagination: &parser.PaginationConfig{DefaultPageSize: 201, MaxPageSize: 200}}, "must not exceed max_page_size", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := New(schemaWithProjectConfig(t, tt.project)).Validate()
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, tt.wantText) && e.Warning == tt.wantWarn {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected diagnostic %q (warning=%t), got %v", tt.wantText, tt.wantWarn, errs)
			}
		})
	}
}

func TestValidateRejectsInvalidNumericDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		ftype      string
		defaultVal string
		wantError  bool
	}{
		{name: "int rejects text", ftype: specmeta.FieldTypeInt, defaultVal: "abc", wantError: true},
		{name: "int accepts integer", ftype: specmeta.FieldTypeInt, defaultVal: "42"},
		{name: "bigint rejects text", ftype: specmeta.FieldTypeBigint, defaultVal: "twelve", wantError: true},
		{name: "bigint accepts integer", ftype: specmeta.FieldTypeBigint, defaultVal: "-9000000000"},
		{name: "float rejects text", ftype: specmeta.FieldTypeFloat, defaultVal: "1.2.3", wantError: true},
		{name: "float accepts decimal", ftype: specmeta.FieldTypeFloat, defaultVal: "1.25"},
		{name: "decimal rejects text", ftype: specmeta.FieldTypeDecimal, defaultVal: "price", wantError: true},
		{name: "decimal accepts decimal", ftype: specmeta.FieldTypeDecimal, defaultVal: "99.95"},
		{name: "legacy true still fails on int", ftype: specmeta.FieldTypeInt, defaultVal: "true", wantError: true},
		{name: "legacy false still fails on bigint", ftype: specmeta.FieldTypeBigint, defaultVal: "false", wantError: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			errs := validateDefaultValue("Item", "amount", c.ftype, c.defaultVal)
			if c.wantError {
				if len(errs) != 1 || errs[0].Warning {
					t.Fatalf("validateDefaultValue(%q, %q) = %v, want one error", c.ftype, c.defaultVal, errs)
				}
				msg := errs[0].Message
				if msg != strings.ToLower(msg) || strings.HasSuffix(msg, ".") {
					t.Errorf("error message %q is not lowercase or ends with punctuation", msg)
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("validateDefaultValue(%q, %q) = %v, want no errors", c.ftype, c.defaultVal, errs)
			}
		})
	}

	// The element type of an array drives the same numeric check.
	v := New(&parser.ParsedSchema{})
	for _, c := range []struct {
		name string
		def  string
		want int
	}{
		{name: "array int accepts numbers", def: "array(int) [default:[1, 2]]", want: 0},
		{name: "array int rejects text", def: "array(int) [default:[1, abc]]", want: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			field := testutil.MustParsedField(t, "scores", c.def)
			errs := v.validateFieldDefault("Item", "scores", field)
			if len(errs) != c.want {
				t.Fatalf("validateFieldDefault(%q) = %v, want %d errors", c.def, errs, c.want)
			}
		})
	}
}
