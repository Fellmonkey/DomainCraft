package testutil

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/parser"
)

func TestMustParsedFieldMatchesParserNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
	}{
		{name: "primary implies required", input: "uuid [primary]"},
		{name: "relation gets relation type", input: "relation(User) [many]"},
		{name: "quoted old name survives", input: `string [required, old_name:"previousTitle"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := MustParsedField(t, "field", c.input)
			want, err := parser.ParseField("field", c.input)
			if err != nil {
				t.Fatalf("parser.ParseField(%q) error = %v", c.input, err)
			}
			if got.IsRequired != want.IsRequired ||
				got.RelationType != want.RelationType ||
				got.DatabaseColumnName != want.DatabaseColumnName ||
				got.OldName != want.OldName {
				t.Errorf("MustParsedField = %#v, want parser.ParseField result %#v", got, want)
			}
		})
	}
}
