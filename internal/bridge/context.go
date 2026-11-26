package bridge

import (
	"context"
	"sync"
)

// commandCtx is cancelled on interrupt so git children and HTTP probes started
// by bridge resolution and updates are never orphaned. main wires the signal
// context.
var commandCtx = context.Background()

// commandCtxMu guards commandCtx so the accessor pair is race-free when the
// bridge package is embedded in a long-running host.
var commandCtxMu sync.RWMutex

// SetCommandContext wires the CLI's cancellable context into bridge operations.
// A nil context is ignored so a caller cannot clear the cancellation signal.
// Concurrent writes are now safe, but callers should prefer passing ctx
// explicitly through the call chain.
func SetCommandContext(ctx context.Context) {
	if ctx != nil {
		commandCtxMu.Lock()
		commandCtx = ctx
		commandCtxMu.Unlock()
	}
}

// CommandContext returns the current command context for read-only use (e.g.
// binding an HTTP request lifetime to the CLI's cancellation). It never mutates
// the package-level context. Concurrent reads are now safe, but callers should
// prefer passing ctx explicitly through the call chain.
func CommandContext() context.Context {
	commandCtxMu.RLock()
	defer commandCtxMu.RUnlock()
	return commandCtx
}
