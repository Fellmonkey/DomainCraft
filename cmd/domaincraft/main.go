package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

func main() {
	// run returns an exit code instead of calling os.Exit, so signal.Notify's
	// stop() defer runs before the process exits.
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	commandCtx = ctx
	bridge.SetCommandContext(ctx)
	if err := Execute(); err != nil {
		return 1
	}
	return 0
}
