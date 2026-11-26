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

// IsUuid reports whether the field holds a UUID. This is the canonical name:
// every shipped bridge and the IR itself use this casing.
func (f Field) IsUuid() bool { return f.DatabaseType == specmeta.FieldTypeUUID } //nolint:revive // bridge template contract casing

// IsText reports whether the field holds a string/text value.
func (f Field) IsText() bool { return specmeta.IsStringType(f.DatabaseType) }

// IsInteger reports whether the field holds an integral number (int or bigint).
func (f Field) IsInteger() bool { return specmeta.IsIntegralType(f.DatabaseType) }

// IsFloat reports whether the field holds a floating-point number (float or decimal).
func (f Field) IsFloat() bool { return specmeta.IsFloatType(f.DatabaseType) }

// IsNumeric reports whether the field holds any numeric type.
func (f Field) IsNumeric() bool { return specmeta.IsNumeric(f.DatabaseType) }

// IsBoolean reports whether the field holds a boolean.
func (f Field) IsBoolean() bool { return f.DatabaseType == specmeta.FieldTypeBoolean }

// IsDate reports whether the field holds a date (no time component).
func (f Field) IsDate() bool { return f.DatabaseType == specmeta.FieldTypeDate }

// IsDateTime reports whether the field holds a datetime.
func (f Field) IsDateTime() bool { return f.DatabaseType == specmeta.FieldTypeDatetime }

// IsJson reports whether the field holds a JSON document (json or jsonb).
func (f Field) IsJson() bool { //nolint:revive // bridge template contract casing
	return f.DatabaseType == specmeta.FieldTypeJSON || f.DatabaseType == specmeta.FieldTypeJSONB
}

// IsJsonB reports whether the field holds a jsonb document specifically
// (as opposed to a plain json column). Bridges use this to choose between
// ordered (numeric) and text-only JSON path operators.
func (f Field) IsJsonB() bool { return f.DatabaseType == specmeta.FieldTypeJSONB } //nolint:revive // bridge template contract casing

// IsString reports whether the field holds a bounded string column
// (as opposed to an unbounded text column, which also satisfies IsText).
func (f Field) IsString() bool { return f.DatabaseType == specmeta.FieldTypeString }

// IsTextOnly reports whether the field holds an unbounded text column
// (as opposed to a bounded string column, which also satisfies IsText).
func (f Field) IsTextOnly() bool { return f.DatabaseType == specmeta.FieldTypeText }

// IsBigInt reports whether the field holds a 64-bit integer (bigint),
// distinct from the 32-bit int.
func (f Field) IsBigInt() bool { return f.DatabaseType == specmeta.FieldTypeBigint }

// IsDecimal reports whether the field holds an arbitrary-precision decimal,
// distinct from the binary float.
func (f Field) IsDecimal() bool { return f.DatabaseType == specmeta.FieldTypeDecimal }

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
