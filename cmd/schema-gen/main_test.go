package main

import "testing"

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
