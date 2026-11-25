package parser

import (
	"reflect"
	"strings"
	"testing"
)

func FuzzParseRawSchema(f *testing.F) {
	for _, seed := range []string{
		"project:\n  name: Test\nentities: {}\n",
		"project:\n  name: Test\nentities:\n  Item:\n    fields:\n      id: uuid [primary]\n",
		"entities: [\n",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		schema, err := ParseRawSchema([]byte(data))
		if err == nil && schema == nil {
			t.Fatal("ParseRawSchema returned neither a schema nor an error")
		}
	})
}

func TestStrictParsingRejectsStructuralShortcuts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		yaml     string
		wantText string
	}{
		{
			name: "fields sequence",
			yaml: `project:
  name: Test
entities:
  Item:
    fields: [id, uuid]
`,
			wantText: "fields must be a mapping",
		},
		{
			name: "unknown key through merge",
			yaml: `project:
  name: Test
  rate_limit:
    <<: &rl
      typo_secret: 1
    permit_limit: 5
entities: {}
`,
			wantText: "field typo_secret not found",
		},
		{
			name: "unknown permission key through merge",
			yaml: `project:
  name: Test
entities:
  Item:
    fields:
      id: uuid [primary]
    permissions:
      <<: &p
        typo: [Admin]
      read: [Admin]
`,
			wantText: "unknown permission key",
		},
		{
			name: "unknown index key through merge",
			yaml: `project:
  name: Test
entities:
  Item:
    fields:
      id: uuid [primary]
    indexes:
      - <<: &i
          typo: true
        fields: [id]
`,
			wantText: "unknown index key",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRawSchema([]byte(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.wantText) {
				t.Fatalf("ParseRawSchema error = %v, want substring %q", err, c.wantText)
			}
		})
	}
}

// TestFieldsYAMLMerge pins the contract for YAML merge keys ("<<") inside an
// entity's `fields` mapping: merged fields come first, in merge order, and a
// directly declared field overrides a merged definition without moving it.
func TestFieldsYAMLMerge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		yaml       string
		wantOrder  []string
		wantFields map[string]string
		wantErr    string
	}{
		{
			name: "anchor mapping merged before own fields",
			yaml: `project:
  name: Test
entities:
  Base:
    fields: &base
      id: uuid [primary]
      createdAt: datetime
  Item:
    fields:
      <<: *base
      name: string [required]
`,
			wantOrder: []string{"id", "createdAt", "name"},
			wantFields: map[string]string{
				"id":        "uuid [primary]",
				"createdAt": "datetime",
				"name":      "string [required]",
			},
		},
		{
			name: "sequence of merge aliases keeps first definition and first position",
			yaml: `project:
  name: Test
entities:
  First:
    fields: &first
      id: uuid [primary]
      title: string [max:10]
  Second:
    fields: &second
      id: int [primary]
      title: string [max:20]
      price: decimal
  Item:
    fields:
      <<: [*first, *second]
      name: string [required]
`,
			wantOrder: []string{"id", "title", "price", "name"},
			wantFields: map[string]string{
				"id":    "uuid [primary]",
				"title": "string [max:10]",
				"price": "decimal",
				"name":  "string [required]",
			},
		},
		{
			name: "direct field overrides merged definition and keeps merged position",
			yaml: `project:
  name: Test
entities:
  Base:
    fields: &base
      id: uuid [primary]
      name: string [max:10]
      price: decimal
  Item:
    fields:
      name: string [required, max:255]
      <<: *base
`,
			wantOrder: []string{"id", "name", "price"},
			wantFields: map[string]string{
				"id":    "uuid [primary]",
				"name":  "string [required, max:255]",
				"price": "decimal",
			},
		},
		{
			name: "unquoted merge with scalar value is an error",
			yaml: `project:
  name: Test
entities:
  Item:
    fields:
      <<: oops
      name: string [required]
`,
			wantErr: "must be a mapping or a sequence of mappings",
		},
		{
			name: "quoted merge key is an ordinary field",
			yaml: `project:
  name: Test
entities:
  Item:
    fields:
      "<<": string
      id: uuid [primary]
`,
			wantOrder: []string{"<<", "id"},
			wantFields: map[string]string{
				"<<": "string",
				"id": "uuid [primary]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema, err := ParseRawSchema([]byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseRawSchema error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRawSchema error = %v", err)
			}
			entity, ok := schema.Entities["Item"]
			if !ok {
				t.Fatalf("entity Item not found in %v", schema.Entities)
			}
			if !reflect.DeepEqual(entity.FieldOrder, tt.wantOrder) {
				t.Errorf("FieldOrder = %v, want %v", entity.FieldOrder, tt.wantOrder)
			}
			if !reflect.DeepEqual(entity.Fields, tt.wantFields) {
				t.Errorf("Fields = %v, want %v", entity.Fields, tt.wantFields)
			}
		})
	}
}

func TestParseNilRawSchema(t *testing.T) {
	t.Parallel()
	if _, err := New(nil).Parse(); err == nil {
		t.Fatal("expected an error for nil raw schema")
	}
}
