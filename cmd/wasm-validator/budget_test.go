package main

import (
	"strings"
	"testing"
)

// TestExportAllocationBudgets gates the deterministic allocation counts of the
// WASM exports in go test. Alternating inputs defeat the single-entry result
// cache, so the measured path is the cold decode Studio hits after an edit.
// Wall-time comparisons stay in bench.yml.
func TestExportAllocationBudgets(t *testing.T) {
	other := strings.Replace(validYAML, "Bench", "Other", 1)
	// Measured on Go 1.27.4: 233 / 7 / 2420 allocs/op. The budgets leave
	// ~25% slack for patch releases but catch a systematic regression.
	cases := []struct {
		name   string
		run    func(int)
		budget float64
	}{
		{
			name: "ValidateYAML-changing",
			run: func(i int) {
				yaml := validYAML
				if i%2 == 1 {
					yaml = other
				}
				_ = ValidateYAML(yaml)
			},
			budget: 300,
		},
		{
			name: "ParseFieldDefinition",
			run: func(int) {
				_ = ParseFieldDefinition("string [required, max:255]", "title")
			},
			budget: 10,
		},
		{
			name: "GenerateYAML",
			run: func(int) {
				_ = GenerateYAML(validYAML, "admin-alpine", "")
			},
			budget: 3500,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range 10 {
				tc.run(i)
			}
			i := 10
			got := testing.AllocsPerRun(20, func() {
				tc.run(i)
				i++
			})
			t.Logf("%s: %g allocs/op", tc.name, got)
			if got > tc.budget {
				t.Errorf("%s allocations = %g, budget = %g", tc.name, got, tc.budget)
			}
		})
	}
}
