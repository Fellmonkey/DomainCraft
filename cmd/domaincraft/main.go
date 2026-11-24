package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	commandCtx = ctx
	bridge.SetCommandContext(ctx)
	Execute()
}
