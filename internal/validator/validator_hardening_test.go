package validator

import (
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/testutil"
)

func TestAuthJWTRequiresEntityAndRoles(t *testing.T) {
	t.Parallel()

	t.Run("no auth entity is an error", func(t *testing.T) {
		t.Parallel()
		schema := &parser.ParsedSchema{
			Project:     parser.ProjectConfig{Name: "Test"},
			Auth:        &parser.AuthConfig{Type: "jwt"},
			EntityOrder: []string{"Item"},
			Entities: map[string]*parser.ParsedEntity{
				"Item": {Name: "Item", FieldOrder: []string{"id"}, Fields: map[string]*parser.ParsedField{
					"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
				}},
			},
		}
		errs := nonWarnings(New(schema).Validate())
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "no entity has both 'email' and 'password'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected hard error about missing auth entity, got %v", errs)
		}
	})

	t.Run("empty roles are an error", func(t *testing.T) {
		t.Parallel()
		schema := &parser.ParsedSchema{
			Project:     parser.ProjectConfig{Name: "Test"},
			Auth:        &parser.AuthConfig{Type: "jwt"},
			EntityOrder: []string{"User"},
			Entities: map[string]*parser.ParsedEntity{
				"User": {Name: "User", FieldOrder: []string{"id", "email", "password"}, Fields: map[string]*parser.ParsedField{
					"id":       testutil.MustParsedField(t, "id", "uuid [primary]"),
					"email":    testutil.MustParsedField(t, "email", "string"),
					"password": testutil.MustParsedField(t, "password", "string"),
				}},
			},
		}
		errs := nonWarnings(New(schema).Validate())
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "auth.roles must declare at least one role") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected hard error about empty auth roles, got %v", errs)
		}
	})
}

func TestRelationPrimaryKeyReportedOnce(t *testing.T) {
	t.Parallel()
	schema := &parser.ParsedSchema{
		Project:     parser.ProjectConfig{Name: "Test"},
		EntityOrder: []string{"User", "Item"},
		Entities: map[string]*parser.ParsedEntity{
			"User": {Name: "User", FieldOrder: []string{"id"}, Fields: map[string]*parser.ParsedField{
				"id": testutil.MustParsedField(t, "id", "uuid [primary]"),
			}},
			"Item": {Name: "Item", FieldOrder: []string{"owner"}, Fields: map[string]*parser.ParsedField{
				"owner": testutil.MustParsedField(t, "owner", "relation(User) [primary]"),
			}},
		},
	}
	errs := nonWarnings(New(schema).Validate())
	count := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "primary key cannot be a relation field") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("got %d relation primary key errors, want 1: %v", count, errs)
	}
}

func TestContradictoryRangePairs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		def     string
		wantErr bool
	}{
		{name: "gte greater than lte", def: "int [gte:10, lte:2]", wantErr: true},
		{name: "gt equal to lt", def: "int [gt:2, lt:2]", wantErr: true},
		{name: "numeric min greater than max", def: "int [min:10, max:2]", wantErr: true},
		{name: "numeric min greater than lte", def: "int [min:100, lte:10]", wantErr: true},
		{name: "gte greater than numeric max", def: "int [gte:100, max:10]", wantErr: true},
		{name: "gt equal to numeric max", def: "int [gt:5, max:5]", wantErr: true},
		{name: "gte equal to lte is valid", def: "int [gte:2, lte:2]"},
		{name: "numeric min below max is valid", def: "int [min:1, max:10]"},
		{name: "numeric min below lte is valid", def: "int [min:1, lte:10]"},
		{name: "gte equal to numeric max is valid", def: "int [gte:2, max:2]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			field := testutil.MustParsedField(t, "value", c.def)
			errs := nonWarnings(validateFieldValidations("Item", "value", field))
			if c.wantErr && len(errs) != 1 {
				t.Fatalf("%s produced %d errors, want 1: %v", c.def, len(errs), errs)
			}
			if !c.wantErr && len(errs) != 0 {
				t.Fatalf("%s produced errors, want none: %v", c.def, errs)
			}
		})
	}
}

func TestStringLengthBoundsMustBeIntegers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		def     string
		wantErr bool
	}{
		{name: "fractional max rejected", def: "string [max:1.5]", wantErr: true},
		{name: "integer max accepted", def: "string [max:5]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			field := testutil.MustParsedField(t, "name", c.def)
			errs := nonWarnings(validateFieldValidations("Item", "name", field))
			if got := len(errs) > 0; got != c.wantErr {
				t.Fatalf("%s errors = %v, wantErr %t", c.def, errs, c.wantErr)
			}
		})
	}
}

func TestInvalidDefaultsAreErrors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		def     string
		wantErr bool
	}{
		{name: "invalid integer default", def: "int [default:abc]", wantErr: true},
		{name: "invalid boolean default", def: "boolean [default:maybe]", wantErr: true},
		{name: "valid boolean default", def: "boolean [default:true]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			field := testutil.MustParsedField(t, "value", c.def)
			errs := New(&parser.ParsedSchema{}).validateFieldDefault("Item", "value", field)
			if got := len(errs) > 0; got != c.wantErr {
				t.Fatalf("%s errors = %v, wantErr %t", c.def, errs, c.wantErr)
			}
			if c.wantErr && errs[0].Warning {
				t.Fatalf("%s produced a warning, want an error", c.def)
			}
		})
	}
}
