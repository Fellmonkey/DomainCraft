//go:build !js || !wasm

package renderer

import "github.com/DomainCraft/DomainCraft/internal/bridge"

func localBridgePaths(input string) (string, string, error) {
	return bridge.LocalBridgePaths(input)
}
