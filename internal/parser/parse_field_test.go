package parser

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

func TestParseFieldNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		fieldName       string
		input           string
		wantRequired    bool
		wantColumn      string
		wantRelationTyp string
	}{
		{
			name:         "primary implies required and required validation",
			fieldName:    "id",
			input:        "uuid [primary]",
			wantRequired: true,
			wantColumn:   "id",
		},
		{
			name:         "required is mirrored into validations",
			fieldName:    "title",
			input:        "string [required]",
			wantRequired: true,
			wantColumn:   "title",
		},
		{
			name:         "optional stays not required",
			fieldName:    "deletedAt",
			input:        "datetime [optional]",
			wantRequired: false,
			wantColumn:   "deleted_at",
		},
		{
			name:            "many relation infers many-to-many",
			fieldName:       "tags",
			input:           "relation(Tag) [many]",
			wantColumn:      "tags",
			wantRelationTyp: "many-to-many",
		},
		{
			name:            "unique relation infers one-to-one",
			fieldName:       "owner",
			input:           "relation(User) [unique]",
			wantColumn:      "owner",
			wantRelationTyp: "one-to-one",
		},
		{
			name:            "plain relation infers many-to-one",
			fieldName:       "categoryId",
			input:           "relation(Category)",
			wantColumn:      "category_id",
			wantRelationTyp: "many-to-one",
		},
		{
			name:       "scalar has no relation type",
			fieldName:  "IPv4Address",
			input:      "string",
			wantColumn: textutil.ToDatabaseColumnName("IPv4Address"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			field, err := ParseField(c.fieldName, c.input)
			if err != nil {
				t.Fatalf("ParseField(%q, %q) error = %v", c.fieldName, c.input, err)
			}
			if field.Name != c.fieldName {
				t.Errorf("Name = %q, want %q", field.Name, c.fieldName)
			}
			if field.IsRequired != c.wantRequired {
				t.Errorf("IsRequired = %v, want %v", field.IsRequired, c.wantRequired)
			}
			if c.wantRequired && field.Validations["required"] != "true" {
				t.Errorf("Validations[required] = %q, want true", field.Validations["required"])
			}
			if !c.wantRequired {
				if _, ok := field.Validations["required"]; ok {
					t.Error("Validations must not contain required when IsRequired is false")
				}
			}
			if field.DatabaseColumnName != c.wantColumn {
				t.Errorf("DatabaseColumnName = %q, want %q", field.DatabaseColumnName, c.wantColumn)
			}
			if field.RelationType != c.wantRelationTyp {
				t.Errorf("RelationType = %q, want %q", field.RelationType, c.wantRelationTyp)
			}
		})
	}
}

func TestParseFieldInvalidDefinition(t *testing.T) {
	t.Parallel()
	if _, err := ParseField("id", "unknown"); err == nil {
		t.Error("ParseField with unknown type should fail")
	}
}
