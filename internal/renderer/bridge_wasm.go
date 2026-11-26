//go:build js && wasm

package renderer

import (
	"fmt"
	"path"
	"path/filepath"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"gopkg.in/yaml.v3"
)

// The browser never calls loadSource (bridges come from the embedded fs.FS),
// but the symbols must exist so the disk and WASM builds share load.go.
func localBridgePaths(input string) (string, string, error) {
	clean := filepath.ToSlash(filepath.Clean(input))
	return clean, path.Join(clean, "bridge.yaml"), nil
}

// parseBridgeContractVersion mirrors bridge.ParseContractVersion without
// importing the network-backed bridge package into the WASM binary.
func parseBridgeContractVersion(data []byte) (int, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0, false, fmt.Errorf("parse bridge contract version: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return 0, false, fmt.Errorf("parse bridge contract version: manifest must be a YAML mapping")
	}
	var cfg struct {
		ContractVersion *int `yaml:"contract_version"`
	}
	if err := doc.Decode(&cfg); err != nil {
		return 0, false, fmt.Errorf("parse bridge contract version: %w", err)
	}
	if cfg.ContractVersion == nil || *cfg.ContractVersion == 0 {
		return specmeta.ContractVersion, false, nil
	}
	if *cfg.ContractVersion < 0 {
		return 0, true, fmt.Errorf("invalid contract_version %d", *cfg.ContractVersion)
	}
	return *cfg.ContractVersion, true, nil
}

func checkBridgeContractVersion(version int) error {
	if version > specmeta.ContractVersion {
		return fmt.Errorf("bridge requires contract v%d, but this core implements contract v%d", version, specmeta.ContractVersion)
	}
	return nil
}
