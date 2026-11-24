package renderer

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"gopkg.in/yaml.v3"
)

// ContractVersion is the version of the bridge contract the core implements. A
// bridge may declare the contract it was authored against via `contract_version`
// in bridge.yaml. The core rejects bridges written against a newer contract and
// treats an absent declaration as this version (with a deprecation notice).
// It aliases specmeta.ContractVersion so the contract has one definition.
const ContractVersion = specmeta.ContractVersion

// sourceConfig is one bridge directory plus its parsed bridge.yaml. A composed
// renderer holds several of these, ordered base-first, with the adapter last.
type sourceConfig struct {
	dir    string
	config BridgeConfig
	// contractDeclared records whether bridge.yaml explicitly declared
	// `contract_version` (vs. being absent → treated as 1).
	contractDeclared bool
}

// Renderer reads bridge.yaml and renders files from IR.
//
// A renderer may be composed from a chain of bridges: the optional base bridges
// (declared via `extends:`) render first, then the adapter on top. Base
// templates, helper templates and type_mappings are all merged; the adapter's
// values win on conflicts.
type Renderer struct {
	sources []sourceConfig // ordered base-first; last entry is the adapter
	log     *logger.Logger
	// migration is the abstract schema-migration plan (set via SetMigration).
	migration *ir.MigrationPlan
	// seedData is the normalized explicit seed (set via SetSeedData).
	seedData *ir.SeedDataset
	// mockData is the deterministic mock seed (set via SetMockData).
	mockData *ir.SeedDataset
}

// config returns the topmost (adapter) bridge config, which owns the effective
// output_dir, name, delimiters and migration commands.
func (r *Renderer) config() BridgeConfig {
	if len(r.sources) == 0 {
		return BridgeConfig{}
	}
	return r.sources[len(r.sources)-1].config
}

// adapterDir returns the directory of the topmost (adapter) bridge.
func (r *Renderer) adapterDir() string {
	if len(r.sources) == 0 {
		return ""
	}
	return r.sources[len(r.sources)-1].dir
}

var layerRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// The closed vocabulary of `when:` conditions. The evaluator in
// shouldRenderContext must use these same names: an unknown condition used to
// fall through its defensive default and render unconditionally.
const (
	whenSeed        = "hasSeed"
	whenEnums       = "hasEnums"
	whenOwnerTokens = "hasOwnerTokens"
	whenAuth        = "hasAuth"
	whenMigration   = "hasMigration"
	whenMockData    = "hasMockData"
	whenAddon       = "hasAddon"
	whenNotAddon    = "notHasAddon"
)

var (
	simpleWhenConditions = []string{whenSeed, whenEnums, whenOwnerTokens, whenAuth, whenMigration, whenMockData, whenAddon, whenNotAddon}
	addonWhenPrefixes    = []string{whenAddon, whenNotAddon}
)

// validateWhenConditions rejects any condition outside the closed vocabulary,
// including unknown colon-prefixed forms such as "hasFeature:audit". Bare
// prefix names stay valid: the evaluator treats them as "any addon".
func validateWhenConditions(bridgeName string, templates []TemplateSpec) error {
	for i, spec := range templates {
		when := spec.When
		if when == "" {
			continue
		}
		prefix, _, hasColon := strings.Cut(when, ":")
		known := false
		if hasColon {
			known = slices.Contains(addonWhenPrefixes, strings.TrimSpace(prefix))
		} else {
			known = slices.Contains(simpleWhenConditions, when)
		}
		if !known {
			return fmt.Errorf("bridge %q template %d: unknown when condition %q (allowed: hasSeed, hasEnums, hasOwnerTokens, hasAuth, hasMigration, hasMockData, hasAddon:<name>, notHasAddon:<name>)", bridgeName, i, when)
		}
	}
	return nil
}

// loadSource loads a single bridge directory (its bridge.yaml).
func loadSource(bridgePath string) (sourceConfig, error) {
	bridgeDir, configPath, err := resolveBridgePath(bridgePath)
	if err != nil {
		return sourceConfig{}, err
	}
	data, err := os.ReadFile(configPath) //nolint:gosec // resolved from validated bridge refs by resolveBridgePath
	if err != nil {
		return sourceConfig{}, fmt.Errorf("read bridge config: %w", err)
	}

	var config BridgeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return sourceConfig{}, fmt.Errorf("parse bridge config: %w", err)
	}
	if config.Layer != "" && !layerRe.MatchString(config.Layer) {
		return sourceConfig{}, fmt.Errorf("invalid layer %q: must match %s", config.Layer, layerRe.String())
	}
	if err := validateWhenConditions(config.Name, config.Templates); err != nil {
		return sourceConfig{}, err
	}
	if config.OutputDir == "" {
		config.OutputDir = "generated"
	}
	// Contract version: use the same parser/gate as the bridge update path so
	// a manifest cannot be accepted on one side and rejected on the other.
	// An absent declaration defaults to the core's version (with a deprecation
	// notice from the caller); a negative or newer declaration is a hard error.
	contractVersion, contractDeclared, err := bridge.ParseContractVersion(data)
	if err != nil {
		return sourceConfig{}, err
	}
	if err := bridge.CheckContractVersion(contractVersion); err != nil {
		return sourceConfig{}, fmt.Errorf("bridge %q: %w", config.Name, err)
	}
	config.ContractVersion = contractVersion
	return sourceConfig{dir: bridgeDir, config: config, contractDeclared: contractDeclared}, nil
}

// New loads a single (leaf) bridge.
func New(bridgePath string, log *logger.Logger) (*Renderer, error) {
	src, err := loadSource(bridgePath)
	if err != nil {
		return nil, err
	}
	if !src.contractDeclared && log != nil {
		log.Warn("bridge %q does not declare `contract_version`; assuming contract v%d (deprecated — please add it)", src.config.Name, src.config.ContractVersion)
	}
	return &Renderer{sources: []sourceConfig{src}, log: log}, nil
}

// Extends reports the base bridge this bridge composes on top of (a path,
// registry ID or owner/repo shorthand), or "" when it is a leaf bridge.
func (r *Renderer) Extends() string {
	return r.config().Extends
}

// AttachBases prepends resolved base bridge directories (outermost base first)
// under this bridge. Bases render before the adapter, and the adapter's
// type_mappings/helpers override the bases' on conflicts.
func (r *Renderer) AttachBases(basePaths []string) error {
	bases := make([]sourceConfig, 0, len(basePaths)+len(r.sources))
	for _, p := range basePaths {
		src, err := loadSource(p)
		if err != nil {
			return err
		}
		bases = append(bases, src)
	}
	r.sources = append(bases, r.sources...)
	return nil
}

// MigrationConfig exposes the bridge's declared database-migration commands
// (from bridge.yaml manifest), used by `domaincraft generate --migrate`.
// It scans the composed chain top-down and returns the first enabled config,
// so a persistence bridge's migrations are found even when the top adapter
// has no migrations (e.g. csharp-rest → csharp-efcore).
func (r *Renderer) MigrationConfig() *MigrationConfig {
	for _, src := range slices.Backward(r.sources) {
		cfg := src.config.Migrations
		if cfg != nil && cfg.Enabled {
			return cfg
		}
	}
	return nil
}

// Layer returns the layer identifier of the topmost bridge.
func (r *Renderer) Layer() string {
	return r.config().Layer
}

// Config returns the topmost bridge config.
func (r *Renderer) Config() BridgeConfig {
	return r.config()
}

// SetMigration attaches the core-computed schema migration plan to this renderer.
func (r *Renderer) SetMigration(plan *ir.MigrationPlan) { r.migration = plan }

// SetSeedData attaches the core-normalized explicit seed to this renderer.
func (r *Renderer) SetSeedData(ds *ir.SeedDataset) { r.seedData = ds }

// SetMockData attaches the core-generated mock seed to this renderer.
func (r *Renderer) SetMockData(ds *ir.SeedDataset) { r.mockData = ds }

// delimiters returns the configured template delimiters, defaulting to ["{{", "}}"].
func (r *Renderer) delimiters() (string, string) {
	if len(r.config().Delimiters) >= 2 {
		return r.config().Delimiters[0], r.config().Delimiters[1]
	}
	return "{{", "}}"
}

// applyDelimiters sets custom delimiters on a template if configured.
func (r *Renderer) applyDelimiters(t *template.Template) *template.Template {
	left, right := r.delimiters()
	if left != "{{" || right != "}}" {
		return t.Delims(left, right)
	}
	return t
}

// resolveBridgePath normalizes a local bridge reference through the shared
// bridge-package helper. It is fail-closed: a missing path or a directory
// without bridge.yaml is an error, never a guessed config path to read.
func resolveBridgePath(bridgePath string) (string, string, error) {
	return bridge.LocalBridgePaths(bridgePath)
}
