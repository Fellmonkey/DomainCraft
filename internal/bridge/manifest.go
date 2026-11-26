package bridge

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest is the identity and registry metadata of bridge.yaml shared by the
// CLI (chain resolution, --replace, lock file) and the contract golden tests.
// The renderer parses the full config — templates, helpers, migrations — in
// its own package; this struct covers only the fields those two consumers read.
type Manifest struct {
	Name             string            `yaml:"name"`
	Layer            string            `yaml:"layer"`
	Version          string            `yaml:"version"`
	Extends          string            `yaml:"extends"`
	ContractVersion  int               `yaml:"contract_version"`
	RegistryURL      string            `yaml:"registry_url"`
	RegistryPackages map[string]string `yaml:"registry_packages"`
	TargetFramework  string            `yaml:"target_framework"`
}

// LoadManifest reads bridge.yaml once from a local bridge directory. The
// contract version goes through ParseContractVersion so an absent/zero field
// resolves to the core's version and a malformed one fails closed — the same
// rule the contract gate applies.
func LoadManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bridge.yaml")) //nolint:gosec // dir comes from validated bridge resolution
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse bridge manifest in %q: %w", dir, err)
	}
	version, _, err := ParseContractVersion(data)
	if err != nil {
		return Manifest{}, err
	}
	m.ContractVersion = version
	return m, nil
}
