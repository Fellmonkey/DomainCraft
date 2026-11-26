//go:build !js || !wasm

package renderer

import "github.com/DomainCraft/DomainCraft/internal/bridge"

func localBridgePaths(input string) (string, string, error) {
	return bridge.LocalBridgePaths(input)
}

func parseBridgeContractVersion(data []byte) (int, bool, error) {
	return bridge.ParseContractVersion(data)
}

func checkBridgeContractVersion(version int) error {
	return bridge.CheckContractVersion(version)
}
