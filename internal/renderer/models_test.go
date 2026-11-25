package renderer

import (
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

func TestSeedReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{
		Entity: &ir.Entity{Name: "Order"},
		SeedData: &ir.SeedDataset{
			Entities: []ir.SeedEntity{
				{
					Name: "Order",
					Records: []ir.SeedRecord{
						{Fields: []ir.SeedValue{{Field: "id", DBType: "uuid", Value: "a"}}},
					},
				},
			},
		},
	}

	got := ctx.Seed()
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	got[0] = ir.SeedRecord{}
	_ = append(got, ir.SeedRecord{})

	records := ctx.SeedData.Entities[0].Records
	if len(records) != 1 {
		t.Fatalf("source records slice changed: got %d, want 1", len(records))
	}
	if len(records[0].Fields) != 1 || records[0].Fields[0].Value != "a" {
		t.Errorf("source record changed: got %+v", records[0])
	}
}
