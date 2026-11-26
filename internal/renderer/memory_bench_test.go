package renderer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

// memoryBenchRenderer builds the same admin-chain renderer for the benchmark
// and the allocation budget. Warm-up before timing compiles the templates once,
// so both measure steady-state generation, not bridge loading.
func memoryBenchRenderer(tb testing.TB) (*Renderer, *ir.Project) {
	tb.Helper()
	fsys := os.DirFS(filepath.Join("..", "..", "bridges"))
	r, err := NewFromFS(fsys, []string{"admin"}, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return r, memoryTestProject(tb)
}

func BenchmarkRenderMemory(b *testing.B) {
	r, project := memoryBenchRenderer(b)
	for range 10 {
		if _, err := r.RenderMemory(project, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.RenderMemory(project, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// TestRenderMemoryAllocationBudget guards the buffer and context allocations
// on the browser-generation path. RenderMemory has no disk or rollback work,
// so its allocation count is stable enough for a CI gate.
func TestRenderMemoryAllocationBudget(t *testing.T) {
	r, project := memoryBenchRenderer(t)
	for range 10 {
		if _, err := r.RenderMemory(project, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := testing.AllocsPerRun(10, func() {
		if _, err := r.RenderMemory(project, nil); err != nil {
			t.Fatal(err)
		}
	})
	// 3595 allocs/op on Go 1.27.4 (3718 under -race). ~25% slack for patch
	// releases; re-introducing per-call template parsing would add thousands.
	const budget = 4700
	t.Logf("RenderMemory/admin: %g allocs/op (budget %g)", got, float64(budget))
	if got > budget {
		t.Errorf("RenderMemory allocations = %g, budget = %g", got, float64(budget))
	}
}
