// Package textutil provides shared string transformation functions
// used by parser, IR builder, and renderer.
package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Pluralize converts a singular noun to its plural form.
// Handles: -y->-ies, -zz->-zzes, -s/-ss/-sh/-x/-z/-ch->-es, -o->-es (after consonant), default -s.
func Pluralize(name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "y") && len(name) > 1 {
		if !isVowel(rune(name[len(name)-2])) {
			return name[:len(name)-1] + "ies"
		}
	}
	if strings.HasSuffix(lower, "zz") {
		return name + "es"
	}
	if strings.HasSuffix(lower, "s") || strings.HasSuffix(lower, "ss") ||
		strings.HasSuffix(lower, "sh") || strings.HasSuffix(lower, "ch") ||
		strings.HasSuffix(lower, "x") || strings.HasSuffix(lower, "z") {
		if strings.HasSuffix(lower, "z") && !strings.HasSuffix(lower, "zz") {
			return name + "zes"
		}
		return name + "es"
	}
	if strings.HasSuffix(lower, "o") && len(name) > 1 {
		if !isVowel(rune(name[len(name)-2])) {
			return name + "es"
		}
	}
	return name + "s"
}

// Singularize converts a plural noun to its singular form.
func Singularize(name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "ies") && len(name) > 3 {
		return name[:len(name)-3] + "y"
	}
	// "Quizzes" -> "Quiz" (zzes -> z)
	if strings.HasSuffix(lower, "zzes") && len(name) > 4 {
		return name[:len(name)-3]
	}
	if strings.HasSuffix(lower, "ses") || strings.HasSuffix(lower, "xes") || strings.HasSuffix(lower, "zes") {
		return name[:len(name)-2]
	}
	if strings.HasSuffix(lower, "s") && len(name) > 1 {
		return name[:len(name)-1]
	}
	return name
}

// PascalCase converts a string to PascalCase.
// Handles snake_case, kebab-case, space-separated, and camelCase boundaries.
func PascalCase(value string) string {
	if value == "" {
		return ""
	}
	// The common path (a plain lowercase identifier) needs one allocation,
	// not the parts slice plus per-part strings of the general path.
	if isSimpleIdentifier(value) {
		b := []byte(value)
		if b[0] >= 'a' && b[0] <= 'z' {
			b[0] -= 'a' - 'A'
		}
		return string(b)
	}
	parts := SplitIdentifier(value)
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		// The first character may be multi-byte; slicing by byte would split a
		// UTF-8 sequence and produce an invalid identifier.
		first, size := utf8.DecodeRuneInString(parts[i])
		parts[i] = string(unicode.ToUpper(first)) + strings.ToLower(parts[i][size:])
	}
	return strings.Join(parts, "")
}

// CamelCase converts a string to camelCase.
func CamelCase(value string) string {
	value = PascalCase(value)
	if value == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToLower(first)) + value[size:]
}

// FKName appends the foreign-key "Id" suffix to a relation field name unless it
// already ends in "Id"/"id". It does NOT apply casing — callers use
// PascalCase/CamelCase/ToDatabaseColumnName as needed (e.g. the C# bridge writes
// `{{ fkName .Name | pascalcase }}` for the property, `ForeignKeyColumnName()`
// for the snake_case DB column).
func FKName(name string) string {
	if strings.HasSuffix(name, "Id") || strings.HasSuffix(name, "id") {
		return name
	}
	return name + "Id"
}

// ToDatabaseColumnName converts camelCase/PascalCase to snake_case.
// Uses SplitIdentifier to correctly handle acronyms (e.g. "HTTPPort" -> "http_port").
func ToDatabaseColumnName(fieldName string) string {
	// Already canonical: no separator, no camel/acronym boundary, no uppercase.
	// Returning the input avoids a parts slice and a Builder allocation.
	if isSimpleIdentifier(fieldName) {
		return fieldName
	}
	return strings.ToLower(strings.Join(SplitIdentifier(fieldName), "_"))
}

// SplitIdentifier splits a string into parts at underscores, hyphens, spaces,
// and camelCase boundaries (e.g. "firstName" -> ["first", "Name"],
// "HTMLParser" -> ["HTML", "Parser"]).
func SplitIdentifier(value string) []string {
	// Plain lowercase ASCII identifiers (the overwhelming majority of field
	// names) have no boundary to split on.
	if isSimpleIdentifier(value) {
		return []string{value}
	}

	// Treat '-' and ' ' as separators in the same pass; the previous two
	// ReplaceAll calls allocated a copy of the whole string each.
	runes := []rune(value)
	parts := make([]string, 0, 4)
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for i, r := range runes {
		switch r {
		case '-', ' ', '_':
			flush()
			continue
		}
		if i > 0 {
			prev := runes[i-1]
			// camelCase boundary: lowercase -> Uppercase
			if unicode.IsUpper(r) && unicode.IsLower(prev) {
				flush()
			}
			// ACRONYM boundary: UPPERCASE -> Uppercase+lowercase (e.g., HTMLParser -> HTML_Parser)
			if i+1 < len(runes) && unicode.IsUpper(r) && unicode.IsUpper(prev) && unicode.IsLower(runes[i+1]) {
				flush()
			}
		}
		cur.WriteRune(r)
	}
	flush()
	if len(parts) == 0 {
		return []string{value}
	}
	return parts
}

// isSimpleIdentifier reports whether value is already in canonical lowercase
// form with no separators and no camel/acronym boundary. Such a string is
// returned unchanged by ToDatabaseColumnName and splits into one part.
func isSimpleIdentifier(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '-' || c == ' ' || c == '_' || c >= utf8.RuneSelf || (c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func isVowel(r rune) bool {
	switch unicode.ToLower(r) {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}
