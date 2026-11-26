package renderer

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"text/template"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"gopkg.in/yaml.v3"
)

// sourceConfig is one bridge directory plus its parsed bridge.yaml. A composed
// renderer holds several of these, ordered base-first, with the adapter last.
type sourceConfig struct {
	dir    string
	config BridgeConfig
	// contractDeclared records whether bridge.yaml explicitly declared
	// `contract_version` (vs. being absent → treated as 1).
	contractDeclared bool
	// readSource loads a bridge file by relative path. Nil means the real
	// filesystem; an embedded fs.FS supplies the in-browser implementation.
	readSource func(dir, rel string) ([]byte, error)
}

// readBridgeFile is the single reader for templates, helpers and mappings.
// Disk sources keep the symlink-safe os.Root path; embedded sources read
// through fs.ReadFile with the same lexical containment rule.
func readBridgeFile(src sourceConfig, rel string) ([]byte, error) {
	if src.readSource != nil {
		return src.readSource(src.dir, rel)
	}
	return readBridgeSource(src.dir, rel)
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
	// beforeCommitHook is a test-only seam called between staging and commit.
	// It lives on the instance, not in package state, so concurrent renders are
	// isolated and no save/restore is needed in tests.
	beforeCommitHook func()
}

// setBeforeCommitHook installs a hook called once between staging and commit.
// Production code never calls it; tests in this package use it to mutate the
// filesystem at the exact point where the commit's path checks must hold.
func (r *Renderer) setBeforeCommitHook(fn func()) { r.beforeCommitHook = fn }

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

// whenSpec is one entry of the closed `when:` vocabulary. Validation and
// evaluation both read this table, so a condition can never be accepted by one
// and rejected (or silently rendered) by the other.
type whenSpec struct {
	eval  func(RenderContext, string) bool
	colon bool // accepts a name after ':' (addon conditions)
}

var whenSpecs = map[string]whenSpec{
	whenSeed:  {eval: func(c RenderContext, _ string) bool { return c.HasSeedData() }},
	whenEnums: {eval: func(c RenderContext, _ string) bool { return c.Project != nil && len(c.Project.Enums) > 0 }},
	whenOwnerTokens: {eval: func(c RenderContext, _ string) bool {
		if c.Project == nil {
			return false
		}
		for _, e := range c.Project.Entities {
			if e.Permissions != nil && e.Permissions.HasOwnerToken() {
				return true
			}
		}
		return false
	}},
	whenAuth:      {eval: func(c RenderContext, _ string) bool { return c.Project != nil && c.Project.HasAuth() }},
	whenMigration: {eval: func(c RenderContext, _ string) bool { return c.HasMigration() }},
	whenMockData:  {eval: func(c RenderContext, _ string) bool { return c.HasMockData() }},
	whenAddon: {colon: true, eval: func(c RenderContext, name string) bool {
		if c.Project == nil {
			return false
		}
		if name == "" {
			return len(c.Project.Addons) > 0
		}
		return c.Project.HasAddon(strings.TrimSpace(name))
	}},
	whenNotAddon: {colon: true, eval: func(c RenderContext, name string) bool {
		if c.Project == nil {
			return true
		}
		if name == "" {
			return len(c.Project.Addons) == 0
		}
		return !c.Project.HasAddon(strings.TrimSpace(name))
	}},
}

// allowedWhenConditions builds the diagnostic from the same table that drives
// validation and evaluation.
func allowedWhenConditions() string {
	names := make([]string, 0, len(whenSpecs))
	for name, spec := range whenSpecs {
		if spec.colon {
			names = append(names, name+":<name>")
		} else {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// validateWhenConditions rejects any condition outside the closed vocabulary,
// including unknown colon-prefixed forms such as "hasFeature:audit". Bare
// addon names stay valid: the evaluator treats them as "any addon".
func validateWhenConditions(bridgeName string, templates []TemplateSpec) error {
	for i, spec := range templates {
		when := spec.When
		if when == "" {
			continue
		}
		prefix, name, hasColon := strings.Cut(when, ":")
		entry, ok := whenSpecs[strings.TrimSpace(prefix)]
		if !ok || (hasColon && !entry.colon) || (!hasColon && strings.TrimSpace(when) != prefix) {
			return fmt.Errorf("bridge %q template %d: unknown when condition %q (allowed: %s)", bridgeName, i, when, allowedWhenConditions())
		}
		_ = name
	}
	return nil
}

// maxParsedSourceCacheEntries bounds the parse cache. In a one-shot CLI it
// lives for one run, but a long-lived host edits bridge.yaml repeatedly and
// would otherwise accumulate one key per edit.
const maxParsedSourceCacheEntries = 64

// parsedSourceCache shares one parsed bridge.yaml between the resolution walk
// (bridgechain asks for Extends) and the render setup (New + AttachBases asks
// for the same configs again). The key includes the file bytes: a bridge
// updated mid-command — by --update-bridges or a manual edit — gets a new key
// and is parsed again, so a cached config can never go stale.
//
// A plain map under an RWMutex makes the cap check + clear + store one atomic
// operation; concurrent loads take the read lock.
var parsedSourceCache = struct {
	sync.RWMutex
	entries map[string]sourceConfig
}{entries: make(map[string]sourceConfig)}

// loadParsedSource looks up one cached config by its content-derived key.
func loadParsedSource(key string) (sourceConfig, bool) {
	parsedSourceCache.RLock()
	src, ok := parsedSourceCache.entries[key]
	parsedSourceCache.RUnlock()
	return src, ok
}

// storeParsedSource inserts into the parse cache, clearing it once the cap is
// reached. Clearing is safe: the key includes the bridge path and the full
// config bytes, so an entry is only ever reused for an identical manifest. No
// correctness depends on older entries — evicting them just makes the next
// identical load re-parse once.
func storeParsedSource(key string, src sourceConfig) {
	parsedSourceCache.Lock()
	defer parsedSourceCache.Unlock()

	if len(parsedSourceCache.entries) >= maxParsedSourceCacheEntries {
		parsedSourceCache.entries = make(map[string]sourceConfig, maxParsedSourceCacheEntries)
	}
	parsedSourceCache.entries[key] = src
}

// loadSource loads a single bridge directory (its bridge.yaml).
func loadSource(bridgePath string) (sourceConfig, error) {
	bridgeDir, configPath, err := localBridgePaths(bridgePath)
	if err != nil {
		return sourceConfig{}, err
	}
	data, err := os.ReadFile(configPath) //nolint:gosec // resolved from validated bridge refs by bridge.LocalBridgePaths
	if err != nil {
		return sourceConfig{}, fmt.Errorf("read bridge config: %w", err)
	}

	key := bridgeDir + "\x00" + configPath + "\x00" + string(data)
	if src, ok := loadParsedSource(key); ok {
		return src, nil
	}

	src, err := parseSourceConfig(data, bridgeDir)
	if err != nil {
		return sourceConfig{}, err
	}
	storeParsedSource(key, src)
	return src, nil
}

// parseSourceConfig validates one bridge.yaml. Both the disk loader and the
// in-memory (embedded fs.FS) loader go through here so contract and condition
// rules can never diverge between CLI and browser generation.
func parseSourceConfig(data []byte, dir string) (sourceConfig, error) {
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
	contractVersion, contractDeclared, err := parseBridgeContractVersion(data)
	if err != nil {
		return sourceConfig{}, err
	}
	if err := checkBridgeContractVersion(contractVersion); err != nil {
		return sourceConfig{}, fmt.Errorf("bridge %q: %w", config.Name, err)
	}
	config.ContractVersion = contractVersion
	return sourceConfig{dir: dir, config: config, contractDeclared: contractDeclared}, nil
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
