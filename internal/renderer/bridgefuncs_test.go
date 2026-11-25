package renderer

import (
	"bytes"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

// testMappings mirrors a typical bridge type_mappings.yaml (C#-flavored,
// matching domaincraft-bridge-csharp).
func testMappings() *typeMappings {
	return &typeMappings{
		Types: map[string]string{
			"uuid":    "Guid",
			"int":     "int",
			"bigint":  "long",
			"decimal": "decimal",
			"string":  "string",
		},
		Behaviors:      map[string]string{"set_null": "SetNull", "cascade": "Cascade"},
		ValueTypes:     []string{"Guid", "int", "long", "decimal", "bool"},
		ArrayFormat:    "List<%s>",
		NullableFormat: "?",
		Literals: map[string]LiteralSpec{
			"string":  {Default: `""`},
			"bigint":  {Suffix: "L", Default: "0L"},
			"decimal": {Suffix: "m", Default: "0m"},
			"uuid":    {Parse: "Guid.Parse(%s)", Default: "Guid.NewGuid()"},
			"json":    {Parse: "JsonDocument.Parse(%s, default)", Default: `JsonDocument.Parse("{}")`},
			"enum":    {Parse: "Enum.Parse<%s>(%s)", Member: "%s.%s"},
		},
		Array:       ArrayLiteralSpec{Open: "new %s { ", Close: " }", Empty: "new %s()"},
		ColumnSizes: map[string]string{"string": "255", "uuid": "36"},
		InputTypes:  map[string]string{"datetime": "DatePicker"},
	}
}

// The FuncMap key set is the bridge contract. Splitting the registrars must
// neither add a core-invented key nor drop a bridge-declared one; columnSize and
// inputType exist only when the bridge declared their mappings.
func TestBuildBridgeFuncsKeySet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		m    *typeMappings
		want []string
	}{
		{name: "nil mappings", m: nil},
		{
			name: "minimal mappings",
			m:    &typeMappings{Types: map[string]string{"int": "int"}},
			want: []string{"arrayLiteralClose", "arrayLiteralOpen", "deleteBehaviorName", "isValueType", "languageType", "literalDefault", "literalMember", "literalValue"},
		},
		{
			name: "full mappings",
			m:    testMappings(),
			want: []string{"arrayLiteralClose", "arrayLiteralOpen", "columnSize", "deleteBehaviorName", "inputType", "isValueType", "languageType", "literalDefault", "literalMember", "literalValue"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := slices.Sorted(maps.Keys(buildBridgeFuncs(tc.m, logger.New())))
			want := slices.Sorted(slices.Values(tc.want))
			if !slices.Equal(got, want) {
				t.Errorf("FuncMap keys = %v, want %v", got, want)
			}
		})
	}
}

func TestBuildBridgeFuncs_NilMappings(t *testing.T) {
	t.Parallel()
	funcs := buildBridgeFuncs(nil, logger.New())
	if _, ok := funcs["languageType"]; ok {
		t.Error("nil mappings should produce no bridge funcs")
	}
}

func TestBuildBridgeFuncs_LanguageType(t *testing.T) {
	t.Parallel()
	fn := buildBridgeFuncs(testMappings(), logger.New())["languageType"].(func(string, bool) string)

	cases := []struct {
		name     string
		dbType   string
		nullable bool
		want     string
	}{
		{"explicit scalar", "uuid", false, "Guid"},
		{"explicit nullable gets suffix", "uuid", true, "Guid?"},
		{"array of mapped inner uses array format", "array(int)", false, "List<int>"},
		{"enum formats via pascalcase", "OrderStatus", false, "OrderStatus"},
		{"nullable enum without enum_nullable stays bare", "OrderStatus", true, "OrderStatus"},
		{"enum array wraps element", "array(OrderStatus)", false, "List<OrderStatus>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := fn(c.dbType, c.nullable); got != c.want {
				t.Errorf("languageType(%q, %v) = %q, want %q", c.dbType, c.nullable, got, c.want)
			}
		})
	}

	m := testMappings()
	m.EnumNullable = true
	fnEnumNullable := buildBridgeFuncs(m, logger.New())["languageType"].(func(string, bool) string)
	if got := fnEnumNullable("OrderStatus", true); got != "OrderStatus?" {
		t.Errorf("nullable enum with enum_nullable = %q, want OrderStatus?", got)
	}

	// Unmapped primitive falls back to the raw type name (with a warning).
	fnWarn := buildBridgeFuncs(&typeMappings{Types: map[string]string{"int": "int"}}, logger.New())["languageType"].(func(string, bool) string)
	if got := fnWarn("boolean", false); got != "boolean" {
		t.Errorf("unmapped primitive = %q, want the raw type name as fallback", got)
	}
}

func TestBuildBridgeFuncs_DefaultArrayFormatAndOptionalFuncs(t *testing.T) {
	t.Parallel()
	m := &typeMappings{Types: map[string]string{"int": "int32"}}
	funcs := buildBridgeFuncs(m, logger.New())

	if _, ok := funcs["columnSize"]; ok {
		t.Error("columnSize must be absent when no column_sizes are declared")
	}
	if _, ok := funcs["inputType"]; ok {
		t.Error("inputType must be absent when no input_types are declared")
	}
	arr := funcs["languageType"].(func(string, bool) string)
	if got := arr("array(int)", false); got != "" {
		t.Errorf("array type without array_format = %q, want an empty expression (no core-side syntax)", got)
	}
	if got := arr("int", true); got != "int32" {
		t.Errorf("empty nullable_format must not append a suffix, got %q", got)
	}
}

// With no literal/array/input specs declared, the core must emit no C# or UI
// syntax of its own. Missing specs produce empty expressions and a warning.
func TestBuildBridgeFuncsRequiresLiteralSpecs(t *testing.T) {
	t.Parallel()
	m := &typeMappings{
		Types:      map[string]string{"int": "MyInt"},
		InputTypes: map[string]string{"string": "textbox"},
	}
	funcs := buildBridgeFuncs(m, logger.New())

	literalDefault := funcs["literalDefault"].(func(string) string)
	for _, dbType := range []string{"boolean", "OrderStatus", "array(int)"} {
		if got := literalDefault(dbType); got != "" {
			t.Errorf("literalDefault(%q) = %q, want an empty expression", dbType, got)
		}
	}
	literalMember := funcs["literalMember"].(func(string, string) string)
	if got := literalMember("OrderStatus", "draft"); got != "" {
		t.Errorf("literalMember = %q, want an empty expression", got)
	}
	arrayOpen := funcs["arrayLiteralOpen"].(func(string) string)
	if got := arrayOpen("array(int)"); got != "" {
		t.Errorf("arrayLiteralOpen = %q, want an empty expression", got)
	}
	if got := funcs["arrayLiteralClose"].(func() string)(); got != "" {
		t.Errorf("arrayLiteralClose = %q, want an empty expression", got)
	}
	languageType := funcs["languageType"].(func(string, bool) string)
	if got := languageType("array(int)", false); got != "" {
		t.Errorf("languageType(array(int)) = %q, want an empty expression without array_format", got)
	}
	inputType := funcs["inputType"].(func(string) string)
	if got := inputType("unknown"); got != "" {
		t.Errorf("inputType(unknown) = %q, want an empty expression", got)
	}

	// A bridge that declares the generic enum zero spec keeps its own syntax:
	// the core reads it from type_mappings.yaml, never hardcodes it.
	withZero := testMappings()
	withZero.Literals["enum"] = LiteralSpec{Zero: "(%s)0"}
	declared := buildBridgeFuncs(withZero, logger.New())["literalDefault"].(func(string) string)
	if got := declared("OrderStatus"); got != "(OrderStatus)0" {
		t.Errorf("declared enum zero = %q, want (OrderStatus)0", got)
	}
}

func TestBuildBridgeFuncs_LiteralValue(t *testing.T) {
	t.Parallel()
	literalValue := buildBridgeFuncs(testMappings(), logger.New())["literalValue"].(func(string, any) string)

	cases := []struct {
		name   string
		dbType string
		value  any
		want   string
	}{
		{"text is JSON-encoded", "string", `hi`, `"hi"`},
		{"numeric suffix", "decimal", "99.99", "99.99m"},
		{"bigint suffix", "bigint", int64(7), "7L"},
		{"uuid parse expression", "uuid", "550e8400-e29b-41d4-a716-446655440000", `Guid.Parse("550e8400-e29b-41d4-a716-446655440000")`},
		{"json parse expression", "json", `{"a":1}`, `JsonDocument.Parse("{\"a\":1}", default)`},
		{"enum parse expression pascalcases the member", "OrderStatus", "draft", `Enum.Parse<OrderStatus>("Draft")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := literalValue(c.dbType, c.value); got != c.want {
				t.Errorf("literalValue(%q, %v) = %q, want %q", c.dbType, c.value, got, c.want)
			}
		})
	}
}

func TestBuildBridgeFuncs_LiteralDefault(t *testing.T) {
	t.Parallel()
	literalDefault := buildBridgeFuncs(testMappings(), logger.New())["literalDefault"].(func(string) string)

	cases := []struct{ name, dbType, want string }{
		{"declared default expression", "uuid", "Guid.NewGuid()"},
		{"scalar default literal", "decimal", "0m"},
		{"no default spec emits no language syntax", "boolean", ""},
		{"no enum zero spec emits no language syntax", "OrderStatus", ""},
		{"empty array literal", "array(int)", "new List<int>()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := literalDefault(c.dbType); got != c.want {
				t.Errorf("literalDefault(%q) = %q, want %q", c.dbType, got, c.want)
			}
		})
	}

	// An enum may declare its own zero-valued member under its type name.
	m := testMappings()
	m.Literals["OrderStatus"] = LiteralSpec{Zero: "%s.None"}
	withZero := buildBridgeFuncs(m, logger.New())["literalDefault"].(func(string) string)
	if got := withZero("OrderStatus"); got != "OrderStatus.None" {
		t.Errorf("enum zero template = %q, want OrderStatus.None", got)
	}
}

func TestBuildBridgeFuncs_LiteralMember(t *testing.T) {
	t.Parallel()
	literalMember := buildBridgeFuncs(testMappings(), logger.New())["literalMember"].(func(string, string) string)

	if got := literalMember("OrderStatus", "in_transit"); got != "OrderStatus.InTransit" {
		t.Errorf("member = %q, want OrderStatus.InTransit", got)
	}
	if got := literalMember("string", "plain"); got != `"plain"` {
		t.Errorf("non-enum member = %q, want the JSON-encoded value", got)
	}

	m := testMappings()
	m.Literals["enum"] = LiteralSpec{} // Member unset → no core-side Type.Member syntax
	fallback := buildBridgeFuncs(m, logger.New())["literalMember"].(func(string, string) string)
	if got := fallback("OrderStatus", "draft"); got != "" {
		t.Errorf("member fallback = %q, want an empty expression", got)
	}
}

func TestBuildBridgeFuncs_ArrayLiteralAndHelpers(t *testing.T) {
	t.Parallel()
	raw := buildBridgeFuncs(testMappings(), logger.New())

	open := raw["arrayLiteralOpen"].(func(string) string)
	if got := open("array(int)"); got != "new List<int> { " {
		t.Errorf("arrayLiteralOpen(array(int)) = %q", got)
	}
	if got := raw["arrayLiteralClose"].(func() string)(); got != " }" {
		t.Errorf("arrayLiteralClose = %q", got)
	}

	isValueType := raw["isValueType"].(func(string) bool)
	if !isValueType("uuid") || !isValueType("bigint") {
		t.Error("isValueType should be true for types mapped into the value_types set")
	}
	if isValueType("string") || isValueType("Unknown") {
		t.Error("isValueType should be false for reference or unmapped types")
	}

	deleteBehaviorName := raw["deleteBehaviorName"].(func(string) string)
	if got := deleteBehaviorName(" Set_Null "); got != "SetNull" {
		t.Errorf("mapped behavior = %q, want SetNull", got)
	}
	if got := deleteBehaviorName("restrict"); got != "Restrict" {
		t.Errorf("unmapped behavior = %q, want the PascalCase fallback Restrict", got)
	}

	columnSize := raw["columnSize"].(func(string) string)
	if got := columnSize("string"); got != "255" {
		t.Errorf("columnSize(string) = %q, want 255", got)
	}
	if got := columnSize("text"); got != "" {
		t.Errorf("columnSize(text) = %q, want empty when undeclared", got)
	}

	inputType := raw["inputType"].(func(string) string)
	if got := inputType("datetime"); got != "DatePicker" {
		t.Errorf("inputType(datetime) = %q, want DatePicker", got)
	}
	if got := inputType("array(datetime)"); got != "DatePicker" {
		t.Errorf("inputType(array(datetime)) = %q, want the inner type's mapping", got)
	}
	if got := inputType("unknown"); got != "" {
		t.Errorf("inputType(unknown) = %q, want an empty UI type when no mapping is declared", got)
	}
}

func TestMergeTypeMappings_AdapterWinsOnConflicts(t *testing.T) {
	t.Parallel()
	base := &typeMappings{
		Types:          map[string]string{"uuid": "Guid", "int": "int"},
		Literals:       map[string]LiteralSpec{"uuid": {Parse: "Base(%s)"}, "decimal": {Suffix: "m"}},
		ArrayFormat:    "IList<%s>",
		NullableFormat: "?",
		EnumNullable:   false,
	}
	overlay := &typeMappings{
		Types:          map[string]string{"int": "Int32"},
		Literals:       map[string]LiteralSpec{"uuid": {Parse: "Adapter(%s)"}},
		NullableFormat: " | null",
		EnumNullable:   true,
	}

	merged := mergeTypeMappings(base, overlay)

	if merged.Types["uuid"] != "Guid" || merged.Types["int"] != "Int32" {
		t.Errorf("types merge = %v, want base kept and adapter override", merged.Types)
	}
	if merged.Literals["uuid"].Parse != "Adapter(%s)" {
		t.Errorf("literals merge: uuid.Parse = %q, want the adapter to win", merged.Literals["uuid"].Parse)
	}
	if merged.Literals["decimal"].Suffix != "m" {
		t.Errorf("literals merge: decimal.Suffix = %q, want the base entry preserved", merged.Literals["decimal"].Suffix)
	}
	if merged.ArrayFormat != "IList<%s>" {
		t.Errorf("array format = %q, want the base value when the adapter declares none", merged.ArrayFormat)
	}
	if merged.NullableFormat != " | null" {
		t.Errorf("nullable format = %q, want the adapter's declared value", merged.NullableFormat)
	}
	if !merged.EnumNullable {
		t.Error("enum_nullable should OR-merge across the chain")
	}
	if mergeMaps[LiteralSpec](nil, nil) != nil {
		t.Error("merging two empty literal maps should yield nil")
	}
}

func TestMergeTypeMappingsAllFields(t *testing.T) {
	t.Parallel()
	base := &typeMappings{
		Types:          map[string]string{"uuid": "BaseId", "int": "int"},
		Behaviors:      map[string]string{"cascade": "BaseCascade", "restrict": "Restrict"},
		ValueTypes:     []string{"BaseId"},
		ArrayFormat:    "BaseArray<%s>",
		NullableFormat: "?",
		InputTypes:     map[string]string{"string": "base-input", "int": "base-number"},
		Literals:       map[string]LiteralSpec{"uuid": {Default: "base"}, "int": {Default: "0"}},
		Array:          ArrayLiteralSpec{Open: "base(", Close: "base)", Empty: "baseEmpty"},
		ColumnSizes:    map[string]string{"string": "100", "uuid": "36"},
	}
	overlay := &typeMappings{
		Types:          map[string]string{"uuid": "AdapterId"},
		Behaviors:      map[string]string{"cascade": "AdapterCascade"},
		InputTypes:     map[string]string{"string": "adapter-input"},
		Literals:       map[string]LiteralSpec{"uuid": {Default: "adapter"}},
		ColumnSizes:    map[string]string{"string": "255"},
		ArrayFormat:    "AdapterArray<%s>",
		NullableFormat: " | null",
		EnumNullable:   true,
		ValueTypes:     []string{"AdapterId"},
		Array:          ArrayLiteralSpec{Open: "adapter(", Close: "adapter)", Empty: "adapterEmpty"},
	}

	merged := mergeTypeMappings(base, overlay)
	if merged.Types["uuid"] != "AdapterId" || merged.Types["int"] != "int" {
		t.Errorf("Types = %v, want adapter override and base entry", merged.Types)
	}
	if merged.Behaviors["cascade"] != "AdapterCascade" || merged.Behaviors["restrict"] != "Restrict" {
		t.Errorf("Behaviors = %v, want adapter override and base entry", merged.Behaviors)
	}
	if merged.InputTypes["string"] != "adapter-input" || merged.InputTypes["int"] != "base-number" {
		t.Errorf("InputTypes = %v, want adapter override and base entry", merged.InputTypes)
	}
	if merged.ColumnSizes["string"] != "255" || merged.ColumnSizes["uuid"] != "36" {
		t.Errorf("ColumnSizes = %v, want adapter override and base entry", merged.ColumnSizes)
	}
	if merged.Literals["uuid"].Default != "adapter" || merged.Literals["int"].Default != "0" {
		t.Errorf("Literals = %v, want adapter override and base entry", merged.Literals)
	}
	if merged.ArrayFormat != "AdapterArray<%s>" || merged.NullableFormat != " | null" ||
		!merged.EnumNullable || len(merged.ValueTypes) != 1 || merged.ValueTypes[0] != "AdapterId" {
		t.Errorf("scalar fields = %+v, want the adapter's values", merged)
	}
	if merged.Array != overlay.Array {
		t.Errorf("Array = %+v, want %+v", merged.Array, overlay.Array)
	}
}

func TestBuildBridgeFuncsWarnsOncePerMissingSpec(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	log := logger.New()
	log.SetWriter(&out)
	m := testMappings()
	delete(m.Types, "jsonb")
	fn := buildBridgeFuncs(m, log)["languageType"].(func(string, bool) string)
	for range 5 {
		fn("jsonb", false)
	}
	if n := strings.Count(out.String(), `no type for "jsonb"`); n != 1 {
		t.Errorf("missing-type warning printed %d times, want 1: %q", n, out.String())
	}
}
