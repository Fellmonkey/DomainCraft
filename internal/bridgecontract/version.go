// Package bridgecontract holds the bridge-contract version gate. It has no
// filesystem or network dependencies, so the CLI, the update path, the disk
// renderer and the in-browser renderer all share one implementation.
package bridgecontract

import (
	"fmt"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"go.yaml.in/yaml/v3"
)

// CoreContractVersion is the bridge-contract version the current core
// implements. It aliases specmeta.ContractVersion so a bump needs one edit.
const CoreContractVersion = specmeta.ContractVersion

// ParseContractVersion reads `contract_version` from bridge.yaml bytes. An
// absent (or zero) field means the core's contract version; every other
// unreadable value — including a negative one — is a hard error.
func ParseContractVersion(data []byte) (int, bool, error) {
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
	if cfg.ContractVersion == nil {
		return CoreContractVersion, false, nil
	}
	if *cfg.ContractVersion < 0 {
		return 0, true, fmt.Errorf("invalid contract_version %d", *cfg.ContractVersion)
	}
	// specmeta owns absent/0 → core version; negative values fail closed above.
	return specmeta.EffectiveContractVersion(*cfg.ContractVersion), *cfg.ContractVersion != 0, nil
}

// CheckContractVersion rejects a bridge declaring a newer contract than the
// core implements.
func CheckContractVersion(contractVersion int) error {
	if contractVersion > CoreContractVersion {
		return fmt.Errorf("bridge requires contract v%d, but this core implements contract v%d", contractVersion, CoreContractVersion)
	}
	return nil
}
