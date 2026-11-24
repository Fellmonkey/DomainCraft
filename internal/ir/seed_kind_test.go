package ir

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

func TestKindOfFollowsSpecmetaClassification(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		specmeta.FieldTypeUUID:     "uuid",
		specmeta.FieldTypeString:   "text",
		specmeta.FieldTypeText:     "text",
		specmeta.FieldTypeInt:      "int",
		specmeta.FieldTypeBigint:   "bigint",
		specmeta.FieldTypeFloat:    "float",
		specmeta.FieldTypeDecimal:  "decimal",
		specmeta.FieldTypeBoolean:  "bool",
		specmeta.FieldTypeDate:     "date",
		specmeta.FieldTypeDatetime: "datetime",
		specmeta.FieldTypeJSON:     "json",
		specmeta.FieldTypeJSONB:    "json",
	}
	for _, typ := range specmeta.PrimitiveFieldTypes {
		expected, ok := want[typ]
		if !ok {
			t.Fatalf("no expected seed kind for primitive type %q", typ)
		}
		if got := KindOf(typ); got != expected {
			t.Errorf("KindOf(%q) = %q, want %q", typ, got, expected)
		}
		arrayType := specmeta.FieldTypeArray + "(" + typ + ")"
		if got := KindOf(arrayType); got != "array" {
			t.Errorf("KindOf(%q) = %q, want array", arrayType, got)
		}
	}
	if got := KindOf("Status"); got != "enum" {
		t.Errorf("KindOf(Status) = %q, want enum", got)
	}
}
