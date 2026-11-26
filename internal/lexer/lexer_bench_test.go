package lexer

import "testing"

func BenchmarkParseDefinition(b *testing.B) {
	for _, tc := range []struct{ name, def string }{
		{name: "simple", def: "string"},
		{name: "modifiers", def: "string [required, unique, min:2, max:255, email, old_name:title]"},
		{name: "relation", def: "relation(Category) [optional, many, on_delete:set_null]"},
		{name: "array-default", def: "array(string) [default:[a, b, c]]"},
		{name: "regex", def: "string [regex:^[a-z]+$]"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Parse(tc.def); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestParseDefinitionAllocationBudget gates the hot field-definition parser in
// go test. The regex case exercises the compile cache: a repeated pattern must
// not allocate a fresh regexp on every Studio keystroke.
func TestParseDefinitionAllocationBudget(t *testing.T) {
	for _, tc := range []struct {
		name, def string
		budget    float64
	}{
		// Measured on Go 1.27.4: 2 / 8 / 8 allocs/op. These paths do no I/O,
		// so allocation counts are stable across OSes and a tight budget
		// catches a single extra allocation in the hot field parser.
		{name: "simple", def: "string", budget: 3},
		{name: "modifiers", def: "string [required, unique, min:2, max:255, email, old_name:title]", budget: 9},
		{name: "regex", def: "string [regex:^[a-z]+$]", budget: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 10 {
				if _, err := Parse(tc.def); err != nil {
					t.Fatal(err)
				}
			}
			got := testing.AllocsPerRun(50, func() {
				if _, err := Parse(tc.def); err != nil {
					t.Fatal(err)
				}
			})
			t.Logf("Parse(%s): %g allocs/op", tc.name, got)
			if got > tc.budget {
				t.Errorf("Parse(%s) allocations = %g, budget = %g", tc.name, got, tc.budget)
			}
		})
	}
}
