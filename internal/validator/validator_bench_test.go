package validator

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/parser"
)

func validatorBenchYAML(n int) string {
	var b strings.Builder
	b.WriteString("project:\n  name: Bench\ndatabase: postgresql\nauth:\n  type: none\nentities:\n")
	for i := range n {
		fmt.Fprintf(&b, "  E%04d:\n    fields:\n      id: uuid [primary]\n      name: string [required, max:255]\n", i)
	}
	return b.String()
}

func validatorBenchSchema(tb testing.TB, yaml string) *parser.ParsedSchema {
	tb.Helper()
	schema, err := parser.ParseYAMLString(yaml)
	if err != nil {
		tb.Fatal(err)
	}
	return schema
}

func validatorKitchenSink(tb testing.TB) string {
	tb.Helper()
	data, err := os.ReadFile("../../compliance-suite/kitchen-sink.yaml")
	if err != nil {
		tb.Fatal(err)
	}
	return string(data)
}

// BenchmarkValidateOnly measures Validate() alone; parsing happens outside the
// timed loop so the benchmark covers only logical validation.
func BenchmarkValidateOnly(b *testing.B) {
	for _, tc := range []struct {
		name   string
		schema *parser.ParsedSchema
	}{
		{name: "kitchen-sink", schema: validatorBenchSchema(b, validatorKitchenSink(b))},
		{name: "entities=100", schema: validatorBenchSchema(b, validatorBenchYAML(100))},
		{name: "entities=1000", schema: validatorBenchSchema(b, validatorBenchYAML(1000))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = New(tc.schema).Validate()
			}
		})
	}
}

// TestValidateAllocationBudget gates the deterministic allocation count in
// go test. Timing comparisons live in bench.yml.
func TestValidateAllocationBudget(t *testing.T) {
	schema := validatorBenchSchema(t, validatorBenchYAML(100))
	for range 10 {
		_ = New(schema).Validate()
	}
	got := testing.AllocsPerRun(30, func() {
		_ = New(schema).Validate()
	})
	// 209 allocs/op measured on Go 1.27.4; 25% slack for patch releases.
	const budget = 270
	t.Logf("Validate/entities=100: %g allocs/op (budget %g)", got, float64(budget))
	if got > budget {
		t.Errorf("Validate allocations = %g, budget = %g", got, float64(budget))
	}
}
