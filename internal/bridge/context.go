package bridge

import "context"

// commandCtx is cancelled on interrupt so git children and HTTP probes started
// by bridge resolution and updates are never orphaned. main wires the signal
// context.
var commandCtx = context.Background()

// SetCommandContext wires the CLI's cancellable context into bridge operations.
// A nil context is ignored so a caller cannot clear the cancellation signal.
func SetCommandContext(ctx context.Context) {
	if ctx != nil {
		commandCtx = ctx
	}
}

// CommandContext returns the current command context for read-only use (e.g.
// binding an HTTP request lifetime to the CLI's cancellation). It never mutates
// the package-level context.
func CommandContext() context.Context {
	return commandCtx
}
