package textutil

import "testing"

func TestSnakeCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"pascal_case", "FirstName", "first_name"},
		{"camel_case", "firstName", "first_name"},
		{"acronym_http_server", "HTTPServer", "http_server"},
		{"acronym_http_status", "HTTPStatus", "http_status"},
		{"acronym_xml_http_request", "XMLHttpRequest", "xml_http_request"},
		{"camel_user_id", "userID", "user_id"},
		{"all_caps", "ALLCAPS", "allcaps"},
		{"acronym_id", "ID", "id"},
		// Digit groups: a number attached to the previous word stays with it,
		// trailing uppercase starts a new word.
		{"digits_with_acronym", "HTTP20xOK", "http_20x_ok"},
		{"digit_group_bld4", "Bld4Floor", "bld4_floor"},
		{"trailing_digits", "ProductV2", "product_v2"},
		{"leading_digits", "V2Product", "v2_product"},
		{"h2o", "H2O", "h2_o"},
		// Documented divergence from ToDatabaseColumnName on acronym+digit runs:
		// SnakeCase splits "I"+"pv4", DB columns keep "IPv4Address" together.
		{"ipv4_acronym_digit", "IPv4Address", "i_pv4_address"},
		// Separators normalize to the connector rune.
		{"snake_case_input", "user_id", "user_id"},
		{"kebab_case_input", "kebab-case-name", "kebab_case_name"},
		{"spaced_input", "already snake_case", "already_snake_case"},
		{"leading_underscores", "__leading", "__leading"},
		{"double_underscore", "double__under", "double__under"},
		// Unicode and CJK non-word characters.
		{"unicode_cjk", "CJK汉字Test", "cjk_汉字_test"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := SnakeCase(c.in); got != c.want {
				t.Errorf("SnakeCase(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestKebabCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"pascal_case", "FirstName", "first-name"},
		{"acronym", "HTTPServer", "http-server"},
		{"digits_with_acronym", "HTTP20xOK", "http-20x-ok"},
		{"digit_group_bld4", "Bld4Floor", "bld4-floor"},
		{"ipv4_acronym_digit", "IPv4Address", "i-pv4-address"},
		{"snake_case_input", "user_id", "user-id"},
		{"spaced_input", "already snake_case", "already-snake-case"},
		{"leading_underscores", "__leading", "--leading"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := KebabCase(c.in); got != c.want {
				t.Errorf("KebabCase(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestToDatabaseColumnName pins the canonical DB column algorithm so the
// template `snakecase` (which diverges on acronym+digit runs, e.g.
// "IPv4Address" -> "i_pv4_address") never silently replaces it.
func TestToDatabaseColumnName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"camel_case", "firstName", "first_name"},
		{"pascal_case", "FirstName", "first_name"},
		{"acronym_http_port", "HTTPPort", "http_port"},
		{"camel_user_id", "userID", "user_id"},
		{"snake_case_input", "user_id", "user_id"},
		{"kebab_case_input", "kebab-case", "kebab_case"},
		{"spaced_input", "spaced name", "spaced_name"},
		{"all_caps", "ALLCAPS", "allcaps"},
		{"product_v2", "ProductV2", "product_v2"},
		{"url_parser", "myURLParser", "my_url_parser"},
		// Acronym+digit run stays glued ("i_pv4address") — see CLAUDE.md;
		// this is why bridges must print DatabaseColumnName instead of
		// re-deriving it with `snakecase`.
		{"ipv4_acronym_digit", "IPv4Address", "i_pv4address"},
		// Empty parts from leading/trailing separators are dropped.
		{"leading_underscores", "__leading", "leading"},
		{"trailing_underscore", "trailing_", "trailing"},
		{"double_underscore", "double__under", "double_under"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := ToDatabaseColumnName(c.in); got != c.want {
				t.Errorf("ToDatabaseColumnName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
