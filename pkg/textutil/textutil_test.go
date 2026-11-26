package textutil

import (
	"testing"
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
