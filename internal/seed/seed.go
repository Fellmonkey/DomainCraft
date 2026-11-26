// Package seed is the core's single source of seed data. Because the core knows
// every field's database type, its validation constraints (min/max, gte/lte,
// email, url, ...) and the relationships between entities, it produces fully
// typed, self-consistent seed records in the IR's SeedValue/SeedRecord shape.
// A bridge only serializes those records into its own literal syntax.
//
// Two producers share the exact same output shape:
//   - Generate  — deterministic MOCK seed, derived from types + constraints +
//     relations (no developer input needed; handy for demos and tests).
//   - Normalize — the developer's explicit `seed:` rows from domain.yaml,
//     coerced to the same typed shape (single serialization path for both).
package seed

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

// Options controls mock-seed generation.
type Options struct {
	// CountPerEntity is how many records to generate per entity (default 2).
	CountPerEntity int
	// Seed offsets the generated counters so different runs (e.g. a "user"
	// fixture vs a "test" fixture) don't collide on unique columns.
	Seed int
}

// Generate builds deterministic mock seed for every entity in the project.
func Generate(project *ir.Project, opts Options) *ir.SeedDataset {
	if project == nil {
		return &ir.SeedDataset{}
	}
	if opts.CountPerEntity <= 0 {
		opts.CountPerEntity = 2
	}

	ds := &ir.SeedDataset{}
	// Primary keys generated so far, keyed by entity name, so FK references can
	// point at real, already-generated parent rows (project.Entities is already
	// topologically sorted by the IR builder).
	generated := map[string][]any{}
	g := &genContext{project: project, generated: generated, counter: opts.Seed}

	for i := range project.Entities {
		entity := &project.Entities[i]
		em := ir.SeedEntity{Name: entity.Name}

		for n := range opts.CountPerEntity {
			rec := ir.SeedRecord{}
			for _, field := range entity.Fields {
				// Sensitive fields (e.g. password) are never auto-mocked — the auth
				// flow owns them and tests create users with explicit credentials.
				// Feature fields (timestamps, version) are server-managed and get
				// their defaults on write, so they are omitted too.
				if field.IsSensitive() || field.IsFeatureField() {
					continue
				}
				rec.Fields = append(rec.Fields, seedValue(field, valueFor(g, field, n)))
			}
			em.Records = append(em.Records, rec)

			// Remember this row's primary key so children can reference it.
			if pk := primaryKey(entity); pk != nil {
				for _, sv := range rec.Fields {
					if sv.Field == pk.Name {
						generated[entity.Name] = append(generated[entity.Name], sv.Value)
					}
				}
			}
		}
		ds.Entities = append(ds.Entities, em)
	}
	return ds
}

// seedValue is the single constructor for a typed seed field, shared by the
// generated and the developer-provided seed paths.
func seedValue(field ir.Field, value any) ir.SeedValue {
	return ir.SeedValue{
		Field:      field.Name,
		DBType:     field.DatabaseType,
		IsRelation: field.IsRelation,
		IsMany:     field.IsMany,
		Value:      value,
	}
}

// Normalize converts the developer's explicit `seed:` rows (raw maps) into the
// same typed SeedRecord shape Generate produces. Values are coerced to native
// Go types based on the field's IR database type, so a bridge serializes both
// explicit and generated seed through one code path.
func Normalize(project *ir.Project) *ir.SeedDataset {
	if project == nil {
		return &ir.SeedDataset{}
	}
	ds := &ir.SeedDataset{}
	for i := range project.Entities {
		entity := &project.Entities[i]
		if len(entity.Seed) == 0 {
			continue
		}
		em := ir.SeedEntity{Name: entity.Name}
		for _, raw := range entity.Seed {
			rec := ir.SeedRecord{}
			// Iterate fields in declaration order for deterministic output; only
			// include fields the developer actually provided a value for.
			for _, field := range entity.Fields {
				val, ok := raw[field.Name]
				if !ok {
					continue
				}
				rec.Fields = append(rec.Fields, seedValue(field, coerceValue(val, field.DatabaseType)))
			}
			em.Records = append(em.Records, rec)
		}
		ds.Entities = append(ds.Entities, em)
	}
	return ds
}

// primaryKey returns the entity's primary key field, or nil.
func primaryKey(entity *ir.Entity) *ir.Field {
	for i := range entity.Fields {
		if entity.Fields[i].IsPrimary {
			return &entity.Fields[i]
		}
	}
	return nil
}

// genContext is the state shared by one mock-seed generation run: the project
// contract, the PKs generated so far (for FK references) and the UUID counter.
type genContext struct {
	project   *ir.Project
	generated map[string][]any
	counter   int
}

// valueFor produces one field value for generated mock seed, honoring type,
// validations, defaults and relations.
func valueFor(g *genContext, field ir.Field, record int) any {
	// Default value wins when the model declares one (and it's not a function).
	if field.DefaultValue != "" && !field.DefaultIsFunc {
		// The lexer stores `default:[a, b]` as a raw string; the same parser
		// is used by coerceValue for developer-provided seed values.
		if out, ok := coerceArrayDefault(field.DefaultValue, field.DatabaseType); ok {
			return out
		}
		return coerceValue(field.DefaultValue, field.DatabaseType)
	}

	if field.IsRelation {
		return relationValue(g, field, record)
	}

	return scalarValue(g, field, record)
}

// relationValue returns a reference to an existing parent row. For a [many]
// collection the value is nil (the child side owns the FK); for a single FK it
// cycles through the parent's primary keys by record index, so a [unique] FK
// can back a one-to-one relation without colliding with a sibling row. When no
// parent was generated yet (self-reference or a dependency cycle) it falls
// back to nil.
func relationValue(g *genContext, field ir.Field, record int) any {
	if field.IsMany {
		return nil
	}
	pks, ok := g.generated[field.RelationTarget]
	if !ok || len(pks) == 0 {
		return nil
	}
	if record < 0 {
		record = 0
	}
	return pks[record%len(pks)]
}

// scalarValue produces a scalar value from the field's type and constraints.
func scalarValue(g *genContext, field ir.Field, record int) any {
	dbType := field.DatabaseType

	if field.IsArray() {
		inner := specmeta.ParseArrayInner(dbType)
		elem := innerValue(g, inner, field, record)
		return []any{elem}
	}

	// FieldKind is the single classification shared with the validator and IR.
	switch specmeta.FieldKind(strings.ToLower(dbType)) {
	case "uuid":
		g.counter++
		return fmt.Sprintf("00000000-0000-0000-0000-%012d", g.counter)
	case "text":
		return stringValue(field, record)
	case "int", "bigint":
		return intValue(field, record)
	case "float", "decimal":
		return floatValue(field, record)
	case "bool":
		return true
	case "date":
		return "2024-01-01"
	case "datetime":
		return "2024-01-01T00:00:00Z"
	case "json":
		return "{}"
	default:
		// Enum (or unknown) — use the first declared value when available.
		if values, ok := g.project.Enums[dbType]; ok && len(values) > 0 {
			return values[0]
		}
		return ""
	}
}

// innerValue produces a scalar for an array element type.
func innerValue(g *genContext, dbType string, field ir.Field, record int) any {
	inner := ir.Field{Name: field.Name, DatabaseType: dbType}
	if specmeta.IsPrimitive(strings.ToLower(dbType)) {
		return scalarValue(g, inner, record)
	}
	if values, ok := g.project.Enums[dbType]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}

// stringValue returns a valid mock string, honoring email/url validations and
// min/max length constraints (via the normalized ValidationRules contract).
func stringValue(field ir.Field, record int) string {
	isEmail, isURL := false, false
	minLen, maxLen := "", ""
	for _, r := range field.ValidationRules() {
		switch r.Kind {
		case "email":
			isEmail = true
		case "url":
			isURL = true
		case "min_length":
			minLen = r.Value
		case "max_length":
			maxLen = r.Value
		}
	}

	var v string
	switch {
	case isEmail:
		v = fmt.Sprintf("mock%d@example.com", record+1)
	case isURL:
		v = fmt.Sprintf("https://example.com/resource%d", record+1)
	default:
		v = fmt.Sprintf("mock_%s_%d", strings.ToLower(field.Name), record+1)
	}

	if n, err := strconv.Atoi(minLen); err == nil && n > 0 && len(v) < n {
		v += strings.Repeat("x", n-len(v))
	}
	// Clamp defensively: a hand-built IR may bypass the validator, so a
	// negative max must never reach a slice index and an oversized max must
	// never exceed the string length.
	if n, err := strconv.Atoi(maxLen); err == nil {
		v = v[:min(max(n, 0), len(v))]
	}
	return v
}

// stepExclusiveInt64 moves one representable unit away from an exclusive
// integer bound. At MaxInt64/MinInt64 there is no next value, so adding or
// subtracting one would wrap to the opposite end; keep the bound instead.
func stepExclusiveInt64(v int64, delta int) int64 {
	if delta > 0 {
		if v == math.MaxInt64 {
			return v
		}
		return v + 1
	}
	if v == math.MinInt64 {
		return v
	}
	return v - 1
}

// stepExclusiveFloat64 moves one unit away from an exclusive float bound.
// Float64 has no integer wraparound.
func stepExclusiveFloat64(v float64, delta int) float64 {
	if delta > 0 {
		return v + 1
	}
	return v - 1
}

// clampNumericBounds applies the normalized min_value/max_value rules to a
// numeric mock value. An exclusive bound steps one unit past the bound; an
// inclusive bound lands on it. Rules are applied in their normalized order,
// each one clamping the current value. Int and float paths share this loop so
// their bound semantics cannot drift apart.
func clampNumericBounds[T ~int64 | ~float64](v T, rules []ir.ValidationRule, parse func(string) (T, error), step func(T, int) T) T {
	for _, r := range rules {
		n, err := parse(r.Value)
		if err != nil {
			continue
		}
		switch {
		case r.Kind == "min_value" && r.Exclusive && v <= n:
			v = step(n, 1)
		case r.Kind == "min_value" && v < n:
			v = n
		case r.Kind == "max_value" && r.Exclusive && v >= n:
			v = step(n, -1)
		case r.Kind == "max_value" && v > n:
			v = n
		}
	}
	return v
}

// intValue returns an int honoring the numeric min/max bounds (inclusive or
// exclusive) from the normalized ValidationRules contract.
func intValue(field ir.Field, record int) int64 {
	parse := func(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
	return clampNumericBounds(int64(record+1), field.ValidationRules(), parse, stepExclusiveInt64)
}

// floatValue returns a float honoring the numeric min/max bounds (inclusive or
// exclusive) from the normalized ValidationRules contract.
func floatValue(field ir.Field, record int) float64 {
	parse := func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
	return clampNumericBounds(float64(record+1)+0.5, field.ValidationRules(), parse, stepExclusiveFloat64)
}

// coerceArrayDefault parses a bracketed array default the same way for
// generated mock data and for developer-provided seed values.
func coerceArrayDefault(raw, dbType string) ([]any, bool) {
	if !specmeta.IsArrayType(dbType) || !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, false
	}
	inner := specmeta.ParseArrayInner(dbType)
	elements := specmeta.SplitArrayDefault(raw)
	out := make([]any, 0, len(elements))
	for _, element := range elements {
		out = append(out, coerceValue(element, inner))
	}
	return out, true
}

// coerceValue converts a declared seed/default value (from YAML: string, number,
// bool, list or map) to the field's native Go type.
func coerceValue(value any, dbType string) any {
	inner := specmeta.ParseArrayInner(dbType)

	// Arrays produce a slice of coerced elements.
	if specmeta.IsArrayType(dbType) {
		if list, ok := value.([]any); ok {
			out := make([]any, 0, len(list))
			for _, item := range list {
				out = append(out, coerceValue(item, inner))
			}
			return out
		}
		if raw, ok := value.(string); ok {
			if out, parsed := coerceArrayDefault(raw, dbType); parsed {
				return out
			}
		}
		return []any{}
	}

	switch specmeta.FieldKind(strings.ToLower(inner)) {
	case "int", "bigint":
		switch v := value.(type) {
		case int:
			return int64(v)
		case int64:
			return v
		case float64:
			// Truncation of a fraction is the established contract; wrapping an
			// out-of-range float is not. Normalize accepts hand-built IR too, so
			// guard the conversion against producing a value nobody declared.
			const twoPow63 = 9223372036854775808.0
			if v >= -twoPow63 && v < twoPow63 {
				return int64(v)
			}
			return int64(0)
		case string:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
		return int64(0)
	case "float", "decimal":
		switch v := value.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
		return float64(0)
	case "bool":
		switch v := value.(type) {
		case bool:
			return v
		case string:
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		}
		return false
	case specmeta.FieldKindJSON:
		return value // keep maps/slices/strings as-is for the bridge's JSON serializer
	default:
		// Enum value name (or unknown) — keep as a string.
		return fmt.Sprintf("%v", value)
	}
}
