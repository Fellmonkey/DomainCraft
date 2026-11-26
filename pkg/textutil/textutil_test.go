package textutil

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestPluralize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"user", "User", "Users"},
		{"category", "Category", "Categories"},
		{"status", "Status", "Statuses"},
		{"box", "Box", "Boxes"},
		{"lady", "Lady", "Ladies"},
		{"piano", "Piano", "Pianoes"},
		{"tax", "Tax", "Taxes"},
		{"quiz", "Quiz", "Quizzes"},
		{"hero", "Hero", "Heroes"},
		{"day_vowel_y", "Day", "Days"},      // y after vowel -> -s
		{"key_vowel_y", "Key", "Keys"},      // y after vowel -> -s
		{"dish_sibilant", "Dish", "Dishes"}, // -sh -> -es
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Pluralize(tt.input)
			if got != tt.want {
				t.Errorf("Pluralize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSingularize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"users", "Users", "User"},
		{"categories", "Categories", "Category"},
		{"statuses", "Statuses", "Status"},
		{"boxes", "Boxes", "Box"},
		{"quizzes", "Quizzes", "Quiz"},
		{"statuses_again", "Statuses", "Status"},
		{"tags", "Tags", "Tag"},
		{"already_singular", "User", "User"}, // no change
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Singularize(tt.input)
			if got != tt.want {
				t.Errorf("Singularize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFKName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "order", "orderId"},
		{"already_suffixed", "orderId", "orderId"},
		{"lowercase_id", "orderid", "orderid"},
		{"acronym", "ipv4Address", "ipv4AddressId"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FKName(tt.input); got != tt.want {
				t.Errorf("FKName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPascalCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"camel_case", "firstName", "FirstName"},
		{"snake_case", "first_name", "FirstName"},
		{"kebab_case", "first-name", "FirstName"},
		{"spaced_input", "first name", "FirstName"},
		{"all_caps", "HTML", "Html"},
		{"empty", "", ""},
		{"single_letter", "a", "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PascalCase(tt.input)
			if got != tt.want {
				t.Errorf("PascalCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCamelCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"snake_case", "first_name", "firstName"},
		{"pascal_case", "FirstName", "firstName"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CamelCase(tt.input)
			if got != tt.want {
				t.Errorf("CamelCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestCasingKeepsUnicodeValid pins the rune boundary: project names are only
// checked for non-emptiness, so a multi-byte first letter must survive casing
// instead of becoming U+FFFD in generated identifiers.
func TestCasingKeepsUnicodeValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		pascal string
		camel  string
	}{
		{"cyrillic", "магазин", "Магазин", "магазин"},
		{"cyrillic snake", "магазин_товары", "МагазинТовары", "магазинТовары"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pascal := PascalCase(tt.input)
			camel := CamelCase(tt.input)
			if pascal != tt.pascal {
				t.Errorf("PascalCase(%q) = %q, want %q", tt.input, pascal, tt.pascal)
			}
			if camel != tt.camel {
				t.Errorf("CamelCase(%q) = %q, want %q", tt.input, camel, tt.camel)
			}
			if !utf8.ValidString(pascal) || !utf8.ValidString(camel) {
				t.Errorf("casing produced invalid UTF-8: %q / %q", pascal, camel)
			}
		})
	}
}

func TestSplitIdentifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"camel_case", "firstName", []string{"first", "Name"}},
		{"snake_case", "first_name", []string{"first", "name"}},
		{"acronym", "HTMLParser", []string{"HTML", "Parser"}},
		{"all_caps_acronym", "HTTPSServer", []string{"HTTPS", "Server"}},
		{"simple", "simple", []string{"simple"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SplitIdentifier(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("SplitIdentifier(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("SplitIdentifier(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// referenceSplitIdentifier is the pre-optimization implementation. Keeping it
// in the test makes the fast path provably behaviour-preserving on a corpus,
// including separators, acronyms and non-ASCII identifiers.
func referenceSplitIdentifier(value string) []string {
	normalized := strings.ReplaceAll(value, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	runes := []rune(normalized)
	var withSeps []rune
	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			if unicode.IsUpper(r) && unicode.IsLower(prev) {
				withSeps = append(withSeps, '_')
			}
			if i+1 < len(runes) && unicode.IsUpper(r) && unicode.IsUpper(prev) && unicode.IsLower(runes[i+1]) {
				withSeps = append(withSeps, '_')
			}
		}
		withSeps = append(withSeps, r)
	}
	parts := strings.Split(string(withSeps), "_")
	var result []string
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return []string{value}
	}
	return result
}

func TestIdentifierTransformsMatchReference(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"", "name", "firstName", "first_name", "first-name", "first name",
		"HTMLParser", "HTTPSServer", "IPv4Address", "HTTP20xOK", "a", "123",
		"__", "--", "a_b-c d", "èCafé_Наименование", "ÜBERStraße", "xY", "XYz",
		"магазин", "магазин_товары",
	} {
		wantParts := referenceSplitIdentifier(value)
		gotParts := SplitIdentifier(value)
		if len(gotParts) != len(wantParts) {
			t.Errorf("SplitIdentifier(%q) = %v, want %v", value, gotParts, wantParts)
			continue
		}
		for i := range wantParts {
			if gotParts[i] != wantParts[i] {
				t.Errorf("SplitIdentifier(%q)[%d] = %q, want %q", value, i, gotParts[i], wantParts[i])
			}
		}

		wantDB := strings.ToLower(strings.Join(wantParts, "_"))
		if got := ToDatabaseColumnName(value); got != wantDB {
			t.Errorf("ToDatabaseColumnName(%q) = %q, want %q", value, got, wantDB)
		}

		wantPascal := ""
		for _, p := range wantParts {
			if p == "" {
				continue
			}
			first, size := utf8.DecodeRuneInString(p)
			wantPascal += string(unicode.ToUpper(first)) + strings.ToLower(p[size:])
		}
		if got := PascalCase(value); got != wantPascal {
			t.Errorf("PascalCase(%q) = %q, want %q", value, got, wantPascal)
		}
	}
}
