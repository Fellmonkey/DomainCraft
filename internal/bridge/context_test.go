package bridge

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestCommandContextConcurrentAccess pins the RWMutex around the package-level
// command context: concurrent Set/Get pairs must finish without racing (run
// with -race). Not parallel: the context is package-global and shared with
// other tests.
func TestCommandContextConcurrentAccess(t *testing.T) {
	original := CommandContext()
	t.Cleanup(func() { SetCommandContext(original) })

	const goroutines = 8
	const iterations = 100
	var wg sync.WaitGroup
	var reads atomic.Int64
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				ctx, cancel := context.WithCancel(context.Background())
				SetCommandContext(ctx)
				if CommandContext() == nil {
					t.Error("CommandContext returned nil after SetCommandContext")
				}
				reads.Add(1)
				cancel()
			}
		})
	}
	wg.Wait()
	if got := reads.Load(); got != goroutines*iterations {
		t.Errorf("completed %d reads, want %d", got, goroutines*iterations)
	}
}
