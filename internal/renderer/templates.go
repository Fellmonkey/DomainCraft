package renderer

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"text/template"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/templatefuncs"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"

	"gopkg.in/yaml.v3"
)

// LiteralSpec declares how a bridge writes a scalar literal of one IR type in
// its target language. Loaded from type_mappings.yaml `literals:` (keyed by IR
// database type; "enum" is a reserved key for user-declared enums).
type LiteralSpec struct {
	// Parse is a printf template (one %s) that turns a serialized value into an
	// expression, e.g. "Guid.Parse(%s)" or "JsonDocument.Parse(%s, default)".
	Parse string `yaml:"parse"`
	// Suffix is appended to a bare numeric literal, e.g. "L" (long) or "m" (decimal).
	Suffix string `yaml:"suffix"`
	// Default is the value-less default expression, e.g. "Guid.NewGuid()" or "0m".
	Default string `yaml:"default"`
	// Member (enums) is a printf template (type, value) for a compile-time member
	// reference, e.g. "%s.%s" -> "OrderStatus.Draft".
	Member string `yaml:"member"`
	// Zero (enums) is a printf template (type) for a zero-valued enum, e.g. "(%s)0".
	Zero string `yaml:"zero"`
}

// ArrayLiteralSpec declares how a bridge writes array literals. %s placeholders
// are filled with the bridge's array type (e.g. "List<int>").
type ArrayLiteralSpec struct {
	Open  string `yaml:"open"`  // e.g. "new %s { "
	Close string `yaml:"close"` // e.g. " }"
	Empty string `yaml:"empty"` // e.g. "new %s()"
}

// typeMappings is the raw shape of a bridge's type_mappings.yaml. A composed
// renderer merges these across its bridge chain (adapter wins on conflicts).
type typeMappings struct {
	Types          map[string]string      `yaml:"types"`
	Behaviors      map[string]string      `yaml:"behaviors"`
	ValueTypes     []string               `yaml:"value_types"`
	ArrayFormat    string                 `yaml:"array_format"`
	EnumNullable   bool                   `yaml:"enum_nullable"`
	NullableFormat string                 `yaml:"nullable_format"`
	InputTypes     map[string]string      `yaml:"input_types"`
	Literals       map[string]LiteralSpec `yaml:"literals"`
	Array          ArrayLiteralSpec       `yaml:"array"`
	ColumnSizes    map[string]string      `yaml:"column_sizes"`
}

func (r *Renderer) buildFuncMap() (template.FuncMap, error) {
	funcMap := templatefuncs.FuncMap()
	// Renderer/IR-specific functions that need renderer context or IR types.
	funcMap["jsonValue"] = jsonValue
	// seedKind maps an IR database type to its language-agnostic value kind
	// (uuid/text/int/.../enum/array), used by bridges that serialize seed values.
	funcMap["seedKind"] = ir.KindOf

	// Bridge-specific functions
	bridgeFuncs, err := r.getBridgeSpecificFuncs()
	if err != nil {
		return nil, err
	}
	maps.Copy(funcMap, bridgeFuncs)
	return funcMap, nil
}

// loadTypeMappings reads a bridge directory's type_mappings.yaml. It returns nil
// (no error) when the file is absent — a bridge with no mappings contributes
// nothing, and the merged result drives the language functions.
func (r *Renderer) loadTypeMappings(dir string) (*typeMappings, error) {
	// Same guard as templates and helpers: a symlinked mapping file must not
	// pull configuration from outside the bridge directory.
	data, err := readBridgeSource(dir, "type_mappings.yaml")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var m typeMappings
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse type_mappings.yaml: %w", err)
	}
	return &m, nil
}

// mergeTypeMappings overlays the adapter's mappings on the base's. Map fields
// merge key-by-key (adapter wins); scalar fields fall back to the base when the
// adapter leaves them zero-valued.
func mergeTypeMappings(base, overlay *typeMappings) *typeMappings {
	if base == nil {
		base = &typeMappings{}
	}
	if overlay == nil {
		overlay = &typeMappings{}
	}
	out := &typeMappings{
		Types:          mergeMaps(base.Types, overlay.Types),
		Behaviors:      mergeMaps(base.Behaviors, overlay.Behaviors),
		InputTypes:     mergeMaps(base.InputTypes, overlay.InputTypes),
		ColumnSizes:    mergeMaps(base.ColumnSizes, overlay.ColumnSizes),
		ArrayFormat:    cmp.Or(overlay.ArrayFormat, base.ArrayFormat),
		NullableFormat: cmp.Or(overlay.NullableFormat, base.NullableFormat),
		EnumNullable:   overlay.EnumNullable || base.EnumNullable,
		ValueTypes:     slices.Clone(overlay.ValueTypes),
		Literals:       mergeMaps(base.Literals, overlay.Literals),
		Array: ArrayLiteralSpec{
			Open:  cmp.Or(overlay.Array.Open, base.Array.Open),
			Close: cmp.Or(overlay.Array.Close, base.Array.Close),
			Empty: cmp.Or(overlay.Array.Empty, base.Array.Empty),
		},
	}
	if len(out.ValueTypes) == 0 {
		out.ValueTypes = slices.Clone(base.ValueTypes)
	}
	return out
}

// mergeMaps overlays src on dst without mutating either input. Two empty maps
// merge to nil so bridges with no mapping keep a zero-valued result.
func mergeMaps[V any](dst, src map[string]V) map[string]V {
	if len(dst) == 0 && len(src) == 0 {
		return nil
	}
	m := make(map[string]V, len(dst)+len(src))
	maps.Copy(m, dst)
	maps.Copy(m, src)
	return m
}

// getBridgeSpecificFuncs loads type_mappings from every bridge in the chain
// (base-first, adapter last) and merges them before building the language
// functions, so an adapter inherits its base's type mapping and can override it.
func (r *Renderer) getBridgeSpecificFuncs() (map[string]any, error) {
	var merged *typeMappings
	for _, src := range r.sources {
		m, err := r.loadTypeMappings(src.dir)
		if err != nil {
			return nil, err
		}
		merged = mergeTypeMappings(merged, m)
	}
	return buildBridgeFuncs(merged, r.log), nil
}

// formatSpec renders a bridge-declared printf spec. An absent spec yields an
// empty expression — the core never substitutes syntax a bridge did not declare.
func formatSpec(format string, args ...any) string {
	if format == "" {
		return ""
	}
	return fmt.Sprintf(format, args...)
}

// warner is the narrow logging surface the bridge funcs need.
type warner interface {
	Warn(format string, args ...any)
}

// dedupWarn emits each identical warning once per Render. A missing spec is
// discovered once per field otherwise, burying the useful diagnostics under
// dozens of identical lines.
type dedupWarn struct {
	mu   sync.Mutex
	log  *logger.Logger
	seen map[string]bool
}

func (w *dedupWarn) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	w.mu.Lock()
	if w.seen[msg] {
		w.mu.Unlock()
		return
	}
	w.seen[msg] = true
	w.mu.Unlock()
	w.log.Warn("%s", msg)
}

// warnMissingSpec reports a spec the bridge must declare in type_mappings.yaml.
// Logger.Warn is nil-safe, so silent mode needs no extra guard.
func warnMissingSpec(w warner, kind, key string) {
	w.Warn("type_mappings.yaml has no %s for %q; emitting an empty expression", kind, key)
}

// buildBridgeFuncs turns a merged type_mappings into the template func map that
// bridges use to map IR types into language-specific code. A nil mapping (no
// type_mappings.yaml anywhere in the chain) yields no bridge-specific funcs.
// Each registrar owns one group of keys; the map keys and closure behavior are
// the contract, so splitting the registrars does not change any value.
func buildBridgeFuncs(m *typeMappings, log *logger.Logger) map[string]any {
	bridgeFuncs := make(map[string]any)
	if m == nil {
		return bridgeFuncs
	}

	w := &dedupWarn{log: log, seen: make(map[string]bool)}
	languageType := registerTypeFuncs(bridgeFuncs, m, w)
	registerLiteralFuncs(bridgeFuncs, m, w, languageType)
	registerValueFuncs(bridgeFuncs, m, w)
	return bridgeFuncs
}

// registerTypeFuncs registers languageType and isValueType. It returns the
// languageType closure so the literal registrars reuse the very same mapping.
func registerTypeFuncs(funcs map[string]any, m *typeMappings, w warner) func(string, bool) string {
	// wrapNullable applies the bridge-specific nullable format to a type name.
	wrapNullable := func(typeName string) string {
		return typeName + m.NullableFormat
	}

	// wrapArray wraps a type name in the bridge-specific array format.
	wrapArray := func(elementType string) string {
		if m.ArrayFormat == "" {
			warnMissingSpec(w, "array_format", elementType)
			return ""
		}
		return fmt.Sprintf(m.ArrayFormat, elementType)
	}

	// languageType maps IR database types to language-specific types via type_mappings.yaml
	languageType := func(dbType string, nullable bool) string {
		// Check explicit type mapping first (covers primitives and arrays)
		if mapped, ok := m.Types[dbType]; ok {
			if nullable && !specmeta.IsArrayType(dbType) {
				return wrapNullable(mapped)
			}
			return mapped
		}

		inner := specmeta.ParseArrayInner(dbType)

		// Enum type (not a primitive, not in mapping) — format for target language
		if !specmeta.IsPrimitive(strings.ToLower(inner)) {
			formatted := textutil.PascalCase(inner)
			if specmeta.IsArrayType(dbType) {
				return wrapArray(formatted)
			}
			if nullable && m.EnumNullable {
				return wrapNullable(formatted)
			}
			return formatted
		}

		// Primitive array not in explicit mapping — wrap inner type
		if specmeta.IsArrayType(dbType) {
			if mapped, ok := m.Types[inner]; ok {
				return wrapArray(mapped)
			}
			w.Warn("type_mappings.yaml has no type for %q; emitting raw type name %q", inner, inner)
			return wrapArray(inner)
		}

		w.Warn("type_mappings.yaml has no type for %q; emitting raw type name %q", dbType, dbType)
		return dbType
	}
	funcs["languageType"] = languageType

	// Build set of value types for quick lookup
	valueTypeSet := make(map[string]bool, len(m.ValueTypes))
	for _, vt := range m.ValueTypes {
		valueTypeSet[vt] = true
	}
	funcs["isValueType"] = func(typeName string) bool {
		if mapped, ok := m.Types[typeName]; ok {
			return valueTypeSet[mapped]
		}
		return false
	}
	return languageType
}

// registerLiteralFuncs registers the literal/array expression funcs. languageType
// is the closure returned by registerTypeFuncs; literal specs are bridge-owned.
func registerLiteralFuncs(funcs map[string]any, m *typeMappings, w warner, languageType func(string, bool) string) {
	// literalValue renders a scalar value of an IR database type as a bridge
	// literal/parse expression. value is a native Go value (string, int64,
	// float64, bool) or the raw string from a domain.yaml default.
	funcs["literalValue"] = func(dbType string, value any) string {
		kind := ir.KindOf(dbType)
		spec := m.Literals[dbType]
		switch kind {
		case "text":
			return jsonValue(value)
		case "int", "bigint", "float", "decimal", "bool":
			return fmt.Sprintf("%v", value) + spec.Suffix
		case "uuid", "date", "datetime", "json":
			if spec.Parse == "" {
				warnMissingSpec(w, "literals.parse", dbType)
				return jsonValue(value) // JSON literal is language-neutral, not a target-language expression
			}
			return formatSpec(spec.Parse, jsonValue(value))
		case "enum":
			e := m.Literals["enum"]
			if e.Parse == "" {
				warnMissingSpec(w, "literals.enum.parse", dbType)
				return jsonValue(value)
			}
			return formatSpec(e.Parse, languageType(dbType, false), jsonValue(textutil.PascalCase(fmt.Sprintf("%v", value))))
		}
		return jsonValue(value)
	}

	// literalDefault renders the value-less default expression for a type:
	// a scalar default, a zero-valued enum, or an empty array. Missing specs
	// produce an empty expression and a warning; the core never invents one.
	funcs["literalDefault"] = func(dbType string) string {
		kind := ir.KindOf(dbType)
		if kind == "array" {
			if m.Array.Empty == "" {
				warnMissingSpec(w, "array.empty", dbType)
				return ""
			}
			return formatSpec(m.Array.Empty, languageType(dbType, false))
		}
		spec := m.Literals[dbType]
		if kind == "enum" {
			if spec.Zero == "" {
				spec = m.Literals["enum"] // generic enum spec covers user-declared enums
			}
			if spec.Zero == "" {
				warnMissingSpec(w, "literals.zero", dbType)
				return ""
			}
			return formatSpec(spec.Zero, languageType(dbType, false))
		}
		if spec.Default == "" {
			warnMissingSpec(w, "literals.default", dbType)
			return ""
		}
		return spec.Default
	}

	// literalMember renders a compile-time enum member reference (Type.Value),
	// used for enum defaults in domain.yaml.
	funcs["literalMember"] = func(dbType string, value string) string {
		if ir.KindOf(dbType) == "enum" {
			spec := m.Literals["enum"]
			if spec.Member == "" {
				warnMissingSpec(w, "literals.enum.member", dbType)
				return ""
			}
			return formatSpec(spec.Member, languageType(dbType, false), textutil.PascalCase(value))
		}
		return jsonValue(value)
	}

	// arrayLiteralOpen/Close wrap rendered elements in the bridge's array
	// syntax (e.g. "new List<int> { " ... " }").
	funcs["arrayLiteralOpen"] = func(dbType string) string {
		if m.Array.Open == "" {
			warnMissingSpec(w, "array.open", dbType)
			return ""
		}
		return formatSpec(m.Array.Open, languageType(dbType, false))
	}
	funcs["arrayLiteralClose"] = func() string {
		return m.Array.Close
	}
}

// registerValueFuncs registers the remaining bridge-specific value/config
// helpers. columnSize and inputType are conditional: they exist only when the
// bridge declared the corresponding mapping (no core-side fallback is added).
func registerValueFuncs(funcs map[string]any, m *typeMappings, w warner) {
	funcs["deleteBehaviorName"] = func(value string) string {
		key := strings.ToLower(strings.TrimSpace(value))
		if v, ok := m.Behaviors[key]; ok {
			return v
		}
		return textutil.PascalCase(value)
	}

	// columnSize returns the bridge's declared default column size for an IR
	// type (e.g. appwrite varchar string -> 255, uuid -> 36), or "" when the
	// bridge declared no size for that type. Sizes are bridge-specific, so
	// they live in type_mappings.yaml `column_sizes:`, keyed by IR type name.
	// The input is the IR type name (element type for arrays, the field type
	// otherwise).
	if len(m.ColumnSizes) > 0 {
		funcs["columnSize"] = func(dbType string) string {
			return m.ColumnSizes[dbType]
		}
	}

	// inputType maps IR database types to UI input components. Bridges that
	// generate UI code declare input_types in their type_mappings.yaml.
	if len(m.InputTypes) > 0 {
		funcs["inputType"] = func(dbType string) string {
			inner := specmeta.ParseArrayInner(dbType)
			if mapped, ok := m.InputTypes[inner]; ok {
				return mapped
			}
			if mapped, ok := m.InputTypes[dbType]; ok {
				return mapped
			}
			warnMissingSpec(w, "input type", dbType)
			return ""
		}
	}
}

// executeTemplateString renders an already-parsed template with one context.
// Target-path patterns are parsed once per spec by the render loop; parsing
// them for every entity would repeat identical work on the hot render path.
func executeTemplateString(tpl *template.Template, data any) (string, error) {
	var builder strings.Builder
	if err := tpl.Execute(&builder, data); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func jsonValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}
