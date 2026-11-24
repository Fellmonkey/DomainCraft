package ir

import (
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/validator"
)

func TestReconciliationRuleAgreesAcrossValidatorAndIR(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		backFields  string
		wantPair    string
		wantWarnKey string
	}{
		{
			name:        "single FK back reconciles",
			backFields:  "\n      order: relation(Order)",
			wantPair:    "order",
			wantWarnKey: "is reconciled with FK 'order'",
		},
		{
			name:       "many back stays many-to-many",
			backFields: "\n      orders: relation(Order) [many]",
		},
		{
			name:       "single FK to another entity stays separate",
			backFields: "\n      company: relation(Company)",
		},
		{
			name: "no back relation",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			yamlData := []byte(`
project:
  name: Test
entities:
  Company:
    fields:
      id: uuid [primary]
  Order:
    fields:
      id: uuid [primary]
      items: relation(User) [many]
  User:
    fields:
      id: uuid [primary]` + c.backFields + "\n")
			schema, err := parser.ParseYAML(yamlData)
			if err != nil {
				t.Fatalf("ParseYAML() error = %v", err)
			}

			warned := false
			for _, e := range validator.New(schema).Validate() {
				if e.Field == "items" && strings.Contains(e.Message, c.wantWarnKey) && c.wantWarnKey != "" {
					warned = true
				}
			}

			project, err := Build(schema)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			var rel *Relation
			for i := range project.Entities {
				if project.Entities[i].Name != "Order" {
					continue
				}
				for j := range project.Entities[i].RelationsOut {
					candidate := &project.Entities[i].RelationsOut[j]
					if candidate.FieldName == "items" {
						rel = candidate
					}
				}
			}
			if rel == nil {
				t.Fatal("Order.items relation not built")
			}
			paired := rel.RelationType == "one-to-many"

			if warned != paired {
				t.Fatalf("validator reconciled = %t, IR reconciled = %t — rule drift", warned, paired)
			}
			if paired && rel.PairFieldName != c.wantPair {
				t.Errorf("PairFieldName = %q, want %q", rel.PairFieldName, c.wantPair)
			}
		})
	}
}
