package bridge

import "context"

// commandCtx is cancelled on interrupt so git children started by bridge
// resolution and updates are never orphaned. main wires the signal context.
var commandCtx = context.Background()

// SetCommandContext wires the CLI's cancellable context into bridge operations.
func SetCommandContext(ctx context.Context) {
	if ctx != nil {
		commandCtx = ctx
	}
}
