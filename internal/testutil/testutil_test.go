package testutil

import "testing"

// Independent oracle: expectations are literals, not the function the helper
// wraps. A regression in parser normalization must fail this test.
func TestMustParsedFieldMatchesParserNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    string
		required bool
		relType  string
		column   string
		oldName  string
	}{
		{name: "primary implies required", input: "uuid [primary]", required: true, column: "field"},
		{name: "relation gets relation type", input: "relation(User) [many]", relType: "many-to-many", column: "field"},
		{name: "quoted old name survives", input: `string [required, old_name:"previousTitle"]`, required: true, column: "field", oldName: "previousTitle"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := MustParsedField(t, "field", c.input)
			if got.IsRequired != c.required || got.RelationType != c.relType ||
				got.DatabaseColumnName != c.column || got.OldName != c.oldName {
				t.Errorf("MustParsedField(%q) = (required=%v, rel=%q, column=%q, old=%q)",
					c.input, got.IsRequired, got.RelationType, got.DatabaseColumnName, got.OldName)
			}
		})
	}
}
