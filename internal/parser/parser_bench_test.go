package parser

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// parserBenchYAML builds a valid model of n entities. It is shared by the
// benchmark and the allocation budget so both measure the same successful path.
func parserBenchYAML(n int) string {
	var b strings.Builder
	b.WriteString("project:\n  name: Bench\ndatabase: postgresql\nauth:\n  type: none\nentities:\n")
	for i := range n {
		fmt.Fprintf(&b, "  E%04d:\n    fields:\n      id: uuid [primary]\n      name: string [required, max:255]\n", i)
	}
	return b.String()
}

func parserKitchenSink(tb testing.TB) string {
	tb.Helper()
	data, err := os.ReadFile("../../compliance-suite/kitchen-sink.yaml")
	if err != nil {
		tb.Fatal(err)
	}
	return string(data)
}

func BenchmarkParseYAMLString(b *testing.B) {
	for _, tc := range []struct {
		name string
		yaml string
	}{
		{name: "small", yaml: "project:\n  name: Bench\nentities:\n  User:\n    fields:\n      id: uuid [primary]\n"},
		{name: "kitchen-sink", yaml: parserKitchenSink(b)},
		{name: "entities=100", yaml: parserBenchYAML(100)},
		{name: "entities=1000", yaml: parserBenchYAML(1000)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ParseYAMLString(tc.yaml); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestParseYAMLStringAllocationBudget gates allocations in go test, where CI
// already runs it. Allocations are deterministic on a fixed input; wall time
// is not, so timing regressions stay in bench.yml's benchstat report.
func TestParseYAMLStringAllocationBudget(t *testing.T) {
	yaml := parserBenchYAML(100)
	for range 10 {
		if _, err := ParseYAMLString(yaml); err != nil {
			t.Fatal(err)
		}
	}
	got := testing.AllocsPerRun(30, func() {
		if _, err := ParseYAMLString(yaml); err != nil {
			t.Fatal(err)
		}
	})
	// 6552 allocs/op measured on Go 1.27.4; 25% slack covers patch releases
	// without letting a systematic regression through.
	const budget = 8200
	t.Logf("ParseYAMLString/entities=100: %g allocs/op (budget %g)", got, float64(budget))
	if got > budget {
		t.Errorf("ParseYAMLString allocations = %g, budget = %g", got, float64(budget))
	}
}
