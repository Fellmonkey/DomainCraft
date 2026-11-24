package ir

import "github.com/DomainCraft/DomainCraft/internal/specmeta"

// IsEnum reports whether the field's effective type — the array element for an
// array field, the type itself otherwise — is a user-declared enum rather than
// a built-in primitive. Bridges use this instead of re-deriving the enum check
// from a raw DatabaseType string.
func (f Field) IsEnum() bool {
	return !specmeta.IsPrimitive(specmeta.ParseArrayInner(f.DatabaseType))
}

// EnumTypeName returns the effective type name of the field: the enum type name
// for an enum field, the element type for an array field, or the field's own
// DatabaseType otherwise. It mirrors the old `enumTypeName` template function.
func (f Field) EnumTypeName() string {
	return specmeta.ParseArrayInner(f.DatabaseType)
}

// The following predicates describe a field's own (non-array) database type, so
// bridges can branch on semantics instead of comparing raw type strings. They
// are false for array fields (whose element type is inspected separately via
// EnumTypeName/ArrayElementType).

// IsUUID reports whether the field holds a UUID.
func (f Field) IsUUID() bool { return f.DatabaseType == "uuid" }

// IsUuid reports whether the field holds a UUID.
//
// Deprecated: use IsUUID. Kept as an alias for bridges using the old casing.
//nolint:revive // deprecated bridge alias; renaming breaks existing bridge templates
func (f Field) IsUuid() bool { return f.IsUUID() }

// IsText reports whether the field holds a string/text value.
func (f Field) IsText() bool { return specmeta.IsStringType(f.DatabaseType) }

// IsInteger reports whether the field holds an integral number (int or bigint).
func (f Field) IsInteger() bool { return specmeta.IsIntegralType(f.DatabaseType) }

// IsFloat reports whether the field holds a floating-point number (float or decimal).
func (f Field) IsFloat() bool { return specmeta.IsFloatType(f.DatabaseType) }

// IsNumeric reports whether the field holds any numeric type.
func (f Field) IsNumeric() bool { return specmeta.IsNumeric(f.DatabaseType) }

// IsBoolean reports whether the field holds a boolean.
func (f Field) IsBoolean() bool { return f.DatabaseType == "boolean" }

// IsDate reports whether the field holds a date (no time component).
func (f Field) IsDate() bool { return f.DatabaseType == "date" }

// IsDateTime reports whether the field holds a datetime.
func (f Field) IsDateTime() bool { return f.DatabaseType == "datetime" }

// IsJSON reports whether the field holds a JSON document (json or jsonb).
func (f Field) IsJSON() bool {
	return f.DatabaseType == "json" || f.DatabaseType == "jsonb"
}

// IsJson reports whether the field holds a JSON document.
//
// Deprecated: use IsJSON.
//nolint:revive // deprecated bridge alias; renaming breaks existing bridge templates
func (f Field) IsJson() bool { return f.IsJSON() }

// IsJSONB reports whether the field holds a jsonb document specifically
// (as opposed to a plain json column). Bridges use this to choose between
// ordered (numeric) and text-only JSON path operators.
func (f Field) IsJSONB() bool { return f.DatabaseType == "jsonb" }

// IsJsonB reports whether the field holds a jsonb document.
//
// Deprecated: use IsJSONB.
//nolint:revive // deprecated bridge alias; renaming breaks existing bridge templates
func (f Field) IsJsonB() bool { return f.IsJSONB() }

// IsString reports whether the field holds a bounded string column
// (as opposed to an unbounded text column, which also satisfies IsText).
func (f Field) IsString() bool { return f.DatabaseType == "string" }

// IsTextOnly reports whether the field holds an unbounded text column
// (as opposed to a bounded string column, which also satisfies IsText).
func (f Field) IsTextOnly() bool { return f.DatabaseType == "text" }

// IsBigInt reports whether the field holds a 64-bit integer (bigint),
// distinct from the 32-bit int.
func (f Field) IsBigInt() bool { return f.DatabaseType == "bigint" }

// IsDecimal reports whether the field holds an arbitrary-precision decimal,
// distinct from the binary float.
func (f Field) IsDecimal() bool { return f.DatabaseType == "decimal" }

// IsPatchable reports whether the field participates in a merge-patch (PATCH)
// request body: scalar fields that are not server-managed, plus single
// (non-collection) foreign keys. Collections, enums, JSON blobs, arrays,
// identity and server-managed fields are excluded.
func (f Field) IsPatchable() bool {
	if f.IsFeatureField() || f.IsSensitive() || f.IsReadonly || f.IsPrimary {
		return false
	}
	if f.IsRelation {
		return !f.IsMany
	}
	return !f.IsArray() && !f.IsEnum() && !f.IsJson()
}
