package main

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

func TestSchemaTopLevelContract(t *testing.T) {
	t.Parallel()
	schema := buildSchema()
	for _, key := range []string{"$schema", "$id", "title", "type", "properties", "required", "$defs"} {
		if _, ok := schema[key]; !ok {
			t.Errorf("buildSchema() is missing %q", key)
		}
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	databases, ok := props["database"].(map[string]any)
	if !ok {
		t.Fatal("schema has no database property")
	}
	values, ok := databases["enum"].([]string)
	if !ok || len(values) == 0 || values[0] != specmeta.Databases[0] {
		t.Fatalf("database enum = %v, want specmeta.Databases", values)
	}
}

// TestSchemaAllowsExplicitZeros pins the schema side of the zero contract: the
// parser and validator treat an explicit 0 in these fields as meaningful, so the
// generated JSON schema must not mark the same document invalid.
func TestSchemaAllowsExplicitZeros(t *testing.T) {
	schema := buildSchema()
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs")
	}

	cases := []struct {
		def   string
		field string
	}{
		{"DeployConfig", "port"},
		{"RateLimitConfig", "permit_limit"},
		{"RateLimitConfig", "window_seconds"},
		{"PaginationConfig", "default_page_size"},
		{"PaginationConfig", "max_page_size"},
	}
	for _, tc := range cases {
		t.Run(tc.def+"."+tc.field, func(t *testing.T) {
			def, ok := defs[tc.def].(map[string]any)
			if !ok {
				t.Fatalf("missing def %s", tc.def)
			}
			props, ok := def["properties"].(map[string]any)
			if !ok {
				t.Fatalf("%s has no properties", tc.def)
			}
			prop, ok := props[tc.field].(map[string]any)
			if !ok {
				t.Fatalf("%s has no property %s", tc.def, tc.field)
			}
			if got := prop["minimum"]; got != 0 {
				t.Errorf("%s.%s minimum = %v, want 0", tc.def, tc.field, got)
			}
		})
	}
}
