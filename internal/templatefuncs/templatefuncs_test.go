package templatefuncs

import (
	"io"
	"reflect"
	"testing"
	"text/template"

	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

func TestContainsAndHasPrefixArgOrder(t *testing.T) {
	t.Parallel()
	// The reversed argument order (substr first) must be preserved.
	if !Contains("ell", "hello") {
		t.Error("Contains('ell', 'hello') = false, want true")
	}
	if !HasPrefix("he", "hello") {
		t.Error("HasPrefix('he', 'hello') = false, want true")
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	got := Add(1, 2, 3)
	if got != 6 {
		t.Errorf("Add(1,2,3) = %d, want 6", got)
	}
}

func TestAtoi(t *testing.T) {
	t.Parallel()
	if got, err := Atoi("42"); err != nil || got != 42 {
		t.Errorf("Atoi('42') = (%d, %v), want (42, nil)", got, err)
	}
	if got, err := Atoi("nope"); err == nil || got != 0 {
		t.Errorf("Atoi('nope') = (%d, %v), want an error", got, err)
	}
	if got := AtoiOrDefault("nope", 7); got != 7 {
		t.Errorf("AtoiOrDefault('nope', 7) = %d, want 7", got)
	}
	if got := AtoiOrDefault("42", 7); got != 42 {
		t.Errorf("AtoiOrDefault('42', 7) = %d, want 42", got)
	}
}

func TestTemplateAtoiKeepsZeroFallback(t *testing.T) {
	t.Parallel()
	fn := FuncMap()["atoi"].(func(string) int)
	if got := fn("nope"); got != 0 {
		t.Errorf("template atoi('nope') = %d, want 0", got)
	}
	if got := fn("42"); got != 42 {
		t.Errorf("template atoi('42') = %d, want 42", got)
	}
}

func TestDefault(t *testing.T) {
	t.Parallel()
	if got := Default("fallback", ""); got != "fallback" {
		t.Errorf("Default(fallback, empty) = %v, want fallback", got)
	}
	if got := Default("fallback", 0); got != "fallback" {
		t.Errorf("Default(fallback, 0) = %v, want fallback", got)
	}
	if got := Default("fallback", "value"); got != "value" {
		t.Errorf("Default(fallback, value) = %v, want value", got)
	}
}

func TestDict(t *testing.T) {
	t.Parallel()
	got := Dict("a", 1, "b", "two")
	want := map[string]any{"a": 1, "b": "two"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Dict = %v, want %v", got, want)
	}
	// Odd trailing key gets empty string.
	got = Dict("k")
	if !reflect.DeepEqual(got, map[string]any{"k": ""}) {
		t.Errorf("Dict(k) = %v, want {k: ''}", got)
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	d := map[string]any{}
	got := Set(d, "x", 7)
	if got["x"] != 7 {
		t.Errorf("Set = %v, want x=7", got)
	}
}

func TestListAppend(t *testing.T) {
	t.Parallel()
	l := List()
	if len(l) != 0 {
		t.Errorf("List() = %v, want empty", l)
	}
	var err error
	l, err = Append(l, "a")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	l, err = Append(l, 2)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !reflect.DeepEqual(l, []any{"a", 2}) {
		t.Errorf("Append chain = %v, want [a 2]", l)
	}
	// Append also works on typed slices (converted via reflection).
	got, err := Append([]string{"x", "y"}, "z")
	if err != nil || !reflect.DeepEqual(got, []any{"x", "y", "z"}) {
		t.Errorf("Append([]string) = (%v, %v), want ([x y z], nil)", got, err)
	}
	if _, err := Append(42, "z"); err == nil {
		t.Error("Append(non-slice) should return an error, not panic")
	}
}

func TestTemplateAppendReturnsErrorOnNonSlice(t *testing.T) {
	t.Parallel()
	fm := FuncMap()
	// Execution of a two-result function is valid in a pipeline; the error
	// surfaces here instead of a panic from the Go helper.
	if err := template.Must(template.New("t").Funcs(fm).Parse(`{{ $_ := append 42 "z" }}`)).Execute(io.Discard, nil); err == nil {
		t.Error("template append on a non-slice should fail")
	}
	if err := template.Must(template.New("t").Funcs(fm).Parse(`{{ $_ := append (list "a") "b" }}`)).Execute(io.Discard, nil); err != nil {
		t.Errorf("template append on a list failed: %v", err)
	}
}

func TestSplitList(t *testing.T) {
	t.Parallel()
	got := SplitList(" ", "a b c")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("SplitList = %v, want [a b c]", got)
	}
}

func TestJoin(t *testing.T) {
	t.Parallel()
	if got := Join(",", []string{"a", "b"}); got != "a,b" {
		t.Errorf("Join = %q, want a,b", got)
	}
	if got := Join("-", []any{1, 2, 3}); got != "1-2-3" {
		t.Errorf("Join([]any) = %q, want 1-2-3", got)
	}
	if got := Join(",", nil); got != "" {
		t.Errorf("Join(nil) = %q, want empty", got)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	if got := KindOf("x"); got != "string" {
		t.Errorf("KindOf('x') = %q, want string", got)
	}
	if got := KindOf([]int{1}); got != "slice" {
		t.Errorf("KindOf([]int{1}) = %q, want slice", got)
	}
}

func TestToString(t *testing.T) {
	t.Parallel()
	if got := ToString("s"); got != "s" {
		t.Errorf("ToString(string) = %q, want s", got)
	}
	if got := ToString(42); got != "42" {
		t.Errorf("ToString(42) = %q, want 42", got)
	}
}

func TestAny(t *testing.T) {
	t.Parallel()
	if Any("", 0, false, nil) {
		t.Error("Any(all empty) = true, want false")
	}
	if !Any("", "x") {
		t.Error("Any('', 'x') = false, want true")
	}
}

func TestFuncMapContainsAllExpected(t *testing.T) {
	t.Parallel()
	fm := FuncMap()
	expected := []string{
		"add", "any", "append", "atoi", "base", "camelcase", "contains",
		"default", "dict", "fkName", "hasPrefix", "humanize", "join",
		"kebabcase", "kindIs", "kindOf", "list", "lower", "lowercase",
		"now", "pascalcase", "pluralize", "set", "snakecase", "splitList",
		"title", "toString", "upper", "uppercase",
	}
	for _, name := range expected {
		if _, ok := fm[name]; !ok {
			t.Errorf("FuncMap missing %q", name)
		}
	}
}

func TestPascalCamelCase(t *testing.T) {
	t.Parallel()
	if got := PascalCase("first_name"); got != "FirstName" {
		t.Errorf("PascalCase(first_name) = %q, want FirstName", got)
	}
	if got := CamelCase("FirstName"); got != "firstName" {
		t.Errorf("CamelCase(FirstName) = %q, want firstName", got)
	}
	// Acronyms must stay split: HTTPPort → HttpPort, not Httpport.
	if got := PascalCase("HTTPPort"); got != "HttpPort" {
		t.Errorf("PascalCase(HTTPPort) = %q, want HttpPort", got)
	}
}

func TestHumanizePluralizeFKName(t *testing.T) {
	t.Parallel()
	if got := Humanize("createdAt"); got != "created At" {
		t.Errorf("Humanize(createdAt) = %q, want 'created At'", got)
	}
	if got := textutil.Pluralize("category"); got != "categories" {
		t.Errorf("pluralize(category) = %q, want categories", got)
	}
	if got := textutil.FKName("Category"); got != "CategoryId" {
		t.Errorf("fkName(Category) = %q, want CategoryId", got)
	}
	if got := textutil.FKName("CategoryId"); got != "CategoryId" {
		t.Errorf("fkName(CategoryId) = %q, want CategoryId", got)
	}
}

func TestSnakeKebabCase(t *testing.T) {
	t.Parallel()
	fm := FuncMap()
	snake := fm["snakecase"].(func(string) string)
	kebab := fm["kebabcase"].(func(string) string)
	cases := []struct {
		name, in, snake, kebab string
	}{
		{"firstName", "FirstName", "first_name", "first-name"},
		{"httpServer", "HTTPServer", "http_server", "http-server"},
		{"noHTTPS", "NoHTTPS", "no_https", "no-https"},
		{"go_path", "GO_PATH", "go_path", "go-path"},
		{"go path", "GO PATH", "go_path", "go-path"},
		{"go-path", "GO-PATH", "go_path", "go-path"},
		{"http2xx", "http2xx", "http_2xx", "http-2xx"},
		{"HTTP20xOK", "HTTP20xOK", "http_20x_ok", "http-20x-ok"},
		{"duration2m3s", "Duration2m3s", "duration_2m3s", "duration-2m3s"},
		{"bld4Floor3rd", "Bld4Floor3rd", "bld4_floor_3rd", "bld4-floor-3rd"},
		{"IPv4Address", "IPv4Address", "i_pv4_address", "i-pv4-address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := snake(c.in); got != c.snake {
				t.Errorf("snakecase(%q) = %q, want %q", c.in, got, c.snake)
			}
			if got := kebab(c.in); got != c.kebab {
				t.Errorf("kebabcase(%q) = %q, want %q", c.in, got, c.kebab)
			}
		})
	}
}
