package parser

import (
	"strings"
	"testing"
)

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

func TestParseNilRawSchema(t *testing.T) {
	t.Parallel()
	if _, err := New(nil).Parse(); err == nil {
		t.Fatal("expected an error for nil raw schema")
	}
}
