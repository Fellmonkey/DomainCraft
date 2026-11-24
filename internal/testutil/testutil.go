// Package testutil provides shared test helpers for DomainCraft packages.
package testutil

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/parser"
)

// MustParsedField parses a field definition the same way the parser does, then
// returns the resulting *ParsedField. Fails the test if parsing fails.
//
// It calls parser.ParseField so test fixtures carry the parser's normalization
// (primary implies required, optional clears required, required is mirrored into
// Validations, and relation fields get their RelationType).
func MustParsedField(t *testing.T, name, input string) *parser.ParsedField {
	t.Helper()
	field, err := parser.ParseField(name, input)
	if err != nil {
		t.Fatalf("ParseField(%q, %q) error = %v", name, input, err)
	}
	return field
}
