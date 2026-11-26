package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// benchmarkYAML builds a synthetic model of n entities. Every entity has one
// optional FK to the first entity, so validation walks both field and relation
// checks without producing errors.
func benchmarkYAML(n int) string {
	var b strings.Builder
	b.WriteString("project:\n  name: Bench\ndatabase: postgresql\nauth:\n  type: none\nentities:\n")
	for i := range n {
		fmt.Fprintf(&b, "  E%04d:\n    fields:\n      id: uuid [primary]\n      name: string [required, max:255]\n", i)
		if i > 0 {
			b.WriteString("      parent: relation(E0000) [optional, on_delete:set_null]\n")
		}
	}
	return b.String()
}

func kitchenSinkYAML(b *testing.B) string {
	b.Helper()
	data, err := os.ReadFile("../../compliance-suite/kitchen-sink.yaml")
	if err != nil {
		b.Fatal(err)
	}
	return string(data)
}

// BenchmarkValidate measures exactly what window.goValidate does: full YAML
// parse, logical validation and JSON serialization of the result.
func BenchmarkValidate(b *testing.B) {
	cases := []struct {
		name string
		yaml func(*testing.B) string
	}{
		{name: "small", yaml: func(*testing.B) string { return validYAML }},
		{name: "kitchen-sink", yaml: kitchenSinkYAML},
		{name: "entities=100", yaml: func(*testing.B) string { return benchmarkYAML(100) }},
		{name: "entities=500", yaml: func(*testing.B) string { return benchmarkYAML(500) }},
		{name: "entities=2000", yaml: func(*testing.B) string { return benchmarkYAML(2000) }},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			// A trailing YAML comment gives the single-entry result cache two
			// distinct keys without producing an invalid document. Appending a
			// digit directly to the YAML made every iteration hit the parse
			// error path and hid the validator entirely.
			base := tc.yaml(b)
			variants := [2]string{base + "\n# v0\n", base + "\n# v1\n"}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				_ = ValidateYAML(variants[i%2])
				i++
			}
		})
	}
}

// BenchmarkParseDomain measures window.goParseDomain: raw YAML decode + marshal.
func BenchmarkParseDomain(b *testing.B) {
	for _, tc := range []struct {
		name string
		yaml func(*testing.B) string
	}{
		{name: "small", yaml: func(*testing.B) string { return validYAML }},
		{name: "kitchen-sink", yaml: kitchenSinkYAML},
		{name: "entities=2000", yaml: func(*testing.B) string { return benchmarkYAML(2000) }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			base := tc.yaml(b)
			variants := [2]string{base + "\n# v0\n", base + "\n# v1\n"}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				_ = ParseDomainYAML(variants[i%2])
				i++
			}
		})
	}
}

// BenchmarkParseField measures window.goParseField across the definition
// shapes the Studio editor produces on every keystroke.
func BenchmarkParseField(b *testing.B) {
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
				_ = ParseFieldDefinition(tc.def, "fieldName")
			}
		})
	}
}

func BenchmarkSpecmeta(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Specmeta()
	}
}

// BenchmarkGenerateYAML measures full in-browser generation: parse, IR build,
// bridge-chain loading and template execution into memory.
func BenchmarkGenerateYAML(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = GenerateYAML(validYAML, "admin-alpine", "")
	}
}

func BenchmarkVersion(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Version("dev")
	}
}

// BenchmarkStudioValidationCycle measures the back-to-back calls the editor
// makes after a model change: validate, parse the raw tree, parse a field.
// "same" is the repeated validation of an unchanged model; "changing" uses a
// fresh YAML string each iteration and exercises the cold decode path.
func BenchmarkStudioValidationCycle(b *testing.B) {
	base := benchmarkYAML(100)
	other := strings.Replace(base, "Bench", "Other", 1)
	for _, tc := range []struct {
		name      string
		alternate bool
	}{
		{name: "same-yaml"},
		{name: "changing-yaml", alternate: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			fieldArgs := []string{"string [required, max:255]", "title"}
			i := 0
			for b.Loop() {
				yaml := base
				if tc.alternate && i%2 == 1 {
					yaml = other
				}
				i++
				_ = ValidateYAML(yaml)
				_ = ParseDomainYAML(yaml)
				_ = ParseFieldDefinition(fieldArgs[0], fieldArgs[1])
			}
		})
	}
}
