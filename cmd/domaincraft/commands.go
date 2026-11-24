// Command domaincraft compiles domain.yaml into code through pluggable bridges.
//
// Commands:
//
//	init     — write a starter domain.yaml from a scenario, then generate
//	generate — parse and validate the model, build IR, render code via a bridge
//	validate — validate domain.yaml without generating
//	bridges  — list known bridges with cache and update status
//	update   — update the core to the latest GitHub release
//
// Global flags: --domain/-d, --bridge/-b, --output/-o, --non-interactive,
// --addons, --update-bridges, --version. Command flags live with their
// constructors below.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/interactive"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/seed"
	"github.com/DomainCraft/DomainCraft/internal/snapshot"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/validator"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultDomainFile = "domain.yaml"
	defaultOutputDir  = "generated"
)

// cliOptions holds the flag values of one command tree. A fresh instance is
// created by every newRootCommand call, so two live trees never clobber each
// other's flags.
type cliOptions struct {
	domainFile          string
	bridgePath          string
	outputDir           string
	nonInteractive      bool
	adminBridge         string // --admin [bridge-id]; empty = not requested
	prune               bool   // --prune: apply migration cleanup without prompting
	addons              string // --addons "dapr,pulsar": comma-separated infrastructure accelerators
	migrate             bool   // --migrate: run the bridge's database-migration commands after generation
	updateBridges       bool   // --update-bridges: download newer bridge versions before generating
	checkUpdates        bool   // bridges --check-updates: contact remotes and report outdated cached bridges
	allowBridgeCommands bool   // explicit consent to run shell lines declared by community bridges
	replaceFlags        []string
}

func newCLIOptions() *cliOptions {
	return &cliOptions{domainFile: defaultDomainFile, outputDir: defaultOutputDir}
}

var (
	// commandCtx is cancelled on interrupt so child processes are not orphaned.
	commandCtx = context.Background()

	// isTerminal is a seam for testing prompt gating without a real TTY.
	isTerminal = interactive.IsTerminal

	// version is stamped at build time via -ldflags "-X main.version=vX.Y.Z" and,
	// when it isn't, backed by the module version in build info (see init).
	version = "dev"
)

// pseudoVersionRe matches Go pseudo-versions (vX.Y.Z-0.yyyymmddhhmmss-hash).
// They appear only in untagged builds — plain `go build` of a checkout — and
// are noise in --version output and confusing for `update`, so they count as
// "dev", not as a released version.
var pseudoVersionRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]+\.[0-9]{14}-[0-9a-f]{12,}(\+[0-9A-Za-z.-]+)?$`)

// resolveVersion picks the version the CLI reports: the value stamped at build
// time wins; otherwise the module version in build info (binaries installed
// with `go install module@vX.Y.Z` embed the clean tag, e.g. v0.5.0); otherwise
// "dev" for a local build.
func resolveVersion(stamped, buildInfo string) string {
	if stamped != "" && stamped != "dev" {
		return stamped
	}
	if buildInfo != "" && buildInfo != "(devel)" && !pseudoVersionRe.MatchString(buildInfo) {
		return buildInfo
	}
	return "dev"
}

// init backs version with build info when it wasn't stamped. The documented
// install path `go install github.com/DomainCraft/DomainCraft/cmd/domaincraft@latest`
// passes no ldflags, so without this fallback an installed binary would report
// "dev" forever (and `domaincraft update` would refuse to self-update).
func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		version = resolveVersion(version, bi.Main.Version)
	}
}

// Execute runs the root command. Cobra is silenced so the error is printed
// exactly once here; the error is returned to main, which owns the exit code.
func Execute() error {
	if err := newRootCommand().Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}
	return nil
}

func newRootCommand() *cobra.Command {
	opts := newCLIOptions()
	rootCmd := &cobra.Command{
		Use:           "domaincraft",
		Short:         "DomainCraft CLI",
		Long:          "DomainCraft CLI — domain-driven code generator.\nParse domain.yaml, validate it, and generate production-ready code via pluggable bridges.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Make the root runnable so Cobra exposes its --version flag, while
			// still showing the help text when invoked without a subcommand.
			return cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVarP(&opts.domainFile, "domain", "d", defaultDomainFile, "path to domain.yaml")
	rootCmd.PersistentFlags().StringVarP(&opts.bridgePath, "bridge", "b", "", "bridge ID, path, or owner/repo (interactive if omitted)")
	rootCmd.PersistentFlags().StringVarP(&opts.outputDir, "output", "o", defaultOutputDir, "output directory")
	rootCmd.PersistentFlags().BoolVar(&opts.nonInteractive, "non-interactive", false, "disable interactive prompts (requires all flags)")
	rootCmd.PersistentFlags().StringVar(&opts.addons, "addons", "", "comma-separated infrastructure addons to enable (e.g. dapr)")
	rootCmd.PersistentFlags().BoolVar(&opts.updateBridges, "update-bridges", false, "check cached bridges for newer versions and download them before generating (no prompts)")

	// Best-effort "new version available" notice on every invocation.
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		maybeNotifyUpdate(cmd)
	}

	for _, cmd := range []*cobra.Command{
		newValidateCmd(opts),
		newInitCmd(opts),
		newGenerateCmd(opts),
		newBridgesCmd(opts),
		newUpdateCmd(),
	} {
		rootCmd.AddCommand(cmd)
	}
	return rootCmd
}

// commandLogger returns a logger writing to the command's error stream. CLI
// progress and diagnostics are not data; stdout stays usable for piping.
func commandLogger(cmd *cobra.Command) *logger.Logger {
	log := logger.New()
	log.SetWriter(cmd.ErrOrStderr())
	return log
}

// --- validate ---

func newValidateCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate domain.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := commandLogger(cmd)
			log.Info("Validating %s", opts.domainFile)
			schema, err := loadAndValidate(opts, cmd)
			if err != nil {
				return err
			}
			log.Success("Schema valid (%d entities)", len(schema.Entities))
			return nil
		},
	}
}

// --- generate ---

func newGenerateCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate code from domain.yaml",
		Long:  "Parse domain.yaml, build IR, and render code via the selected bridge.\nIf --bridge is omitted, an interactive selection menu is shown.\nUse --admin to also generate an admin panel (optionally specify a bridge ID).",
		// One positional argument is the optional value of `--admin <id>`;
		// `generate` itself takes none. runGenerate validates that pairing.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerate(opts, cmd, args)
		},
	}

	// --admin [bridge-id] — optional value, defaults to "admin-alpine" when flag
	// is present without a value. NoOptDefVal is used for the default, and the
	// RunE handler reclaims a space-separated value from args so both
	// `--admin <id>` and `--admin=<id>` work.
	cmd.Flags().StringVar(&opts.adminBridge, "admin", "", "generate admin panel (optionally specify bridge ID, default: admin-alpine)")
	cmd.Flags().Lookup("admin").NoOptDefVal = "admin-alpine"
	// --prune — automatically delete/rename orphaned files without prompting (CI).
	cmd.Flags().BoolVar(&opts.prune, "prune", false, "automatically remove/rename orphaned files and rewrite renamed identifiers in custom files, then run the bridge's database migrations (best-effort) when the schema changed (no prompts; CI-safe)")
	// --migrate — after generation, run the bridge's declared database-migration commands.
	cmd.Flags().BoolVar(&opts.migrate, "migrate", false, "run the bridge's database-migration commands after generation (e.g. dotnet ef database update)")
	cmd.Flags().BoolVar(&opts.allowBridgeCommands, "allow-bridge-commands", false, "allow shell commands declared by community bridges (required with --migrate or automatic --prune migrations)")
	cmd.Flags().StringArrayVar(&opts.replaceFlags, "replace", nil, "replace a bridge in the extends chain: layer=bridgeRef or bridgeId=bridgeRef (repeatable)")

	return cmd
}

// runGenerate is shared by `generate` and `init --bridge` so both paths go
// through the same validation, rendering and migration pipeline.
func runGenerate(opts *cliOptions, cmd *cobra.Command, args []string) error {
	log := commandLogger(cmd)

	log.Info("Parsing %s", opts.domainFile)
	schema, err := loadAndValidate(opts, cmd)
	if err != nil {
		return err
	}
	log.Success("Schema valid (%d entities)", len(schema.Entities))

	resolvedPath, bridgeName, err := resolveBridgeInteractive(opts, log)
	if err != nil {
		return err
	}
	opts.bridgePath = resolvedPath

	log.Info("Building IR")
	irProject, err := ir.Build(schema)
	if err != nil {
		return err
	}
	applyAddons(opts, irProject)

	// --- Schema snapshot / migration diff ---
	snapPath := snapshot.SnapshotPath(opts.outputDir)
	oldSnapshot, err := snapshot.Load(snapPath)
	if err != nil {
		return fmt.Errorf("load schema snapshot: %w", err)
	}
	migrationDiff := snapshot.ComputeDiff(oldSnapshot, irProject, opts.outputDir)

	// --- Abstract schema migration plan (smart core / dumb bridge) ---
	// The core compares the previous model against the current one and
	// emits a language-agnostic migration tree the bridge renders.
	migrationPlan := snapshot.ComputeMigrationPlan(oldSnapshot, irProject)

	// --- Seed data (smart core / dumb bridge) ---
	// Two producers share one typed shape (ir.SeedRecord): the developer's
	// explicit `seed:` rows (Normalize) and deterministic generated mock
	// seed (Generate). A bridge serializes both through one code path.
	seedData := seed.Normalize(irProject)
	mockDataset := seed.Generate(irProject, seed.Options{})

	// Parse --replace flags (layer|bridgeId=bridgeRef).
	replacements, err := parseReplaceFlags(opts.replaceFlags)
	if err != nil {
		return err
	}
	// Resolve extends chain with optional node replacements.
	newBridgePath, basePaths, err := resolveChainWithReplacements(opts, opts.bridgePath, replacements, log)
	if err != nil {
		return err
	}
	opts.bridgePath = newBridgePath
	// Phase 3.5: a community bridge (not a bundled official bridge) must
	// never have migrations auto-run — that would execute arbitrary shell
	// lines from an untrusted bridge.yaml during a code-generation run.
	chainHasCommunity := false
	for _, p := range append([]string{opts.bridgePath}, basePaths...) {
		if !bridge.IsOfficialBundled(p) {
			chainHasCommunity = true
			break
		}
	}
	log.Info("Rendering via %s", opts.bridgePath)
	rendererInstance, err := renderer.New(opts.bridgePath, log)
	if err != nil {
		return err
	}
	if err := rendererInstance.AttachBases(basePaths); err != nil {
		return err
	}
	rendererInstance.SetMigration(migrationPlan)
	rendererInstance.SetSeedData(seedData)
	rendererInstance.SetMockData(mockDataset)

	writtenFiles, manifest, err := rendererInstance.Render(irProject, opts.outputDir)
	if err != nil {
		return err
	}

	log.Success("Generated %d file(s) into %s", len(writtenFiles), opts.outputDir)

	// --- Admin panel generation ---
	// pflag's NoOptDefVal makes `--admin <id>` stop consuming the next
	// argument (it lands in args), so reclaim it here. This keeps both
	// `--admin <path>` and `--admin=<path>` working while `--admin` alone
	// still means the default admin-alpine bridge. Any other positional
	// argument is rejected by cobra.MaximumNArgs(1) / the check below.
	if len(args) > 0 {
		if !cmd.Flags().Changed("admin") || opts.adminBridge != "admin-alpine" {
			return fmt.Errorf("unexpected positional argument %q: use --admin <bridge-id>", args[0])
		}
		opts.adminBridge = args[0]
	}
	if opts.adminBridge == "" && !cmd.Flags().Changed("admin") && !opts.nonInteractive && isTerminal() {
		generate, err := interactive.PromptGenerateAdmin()
		if err != nil {
			return fmt.Errorf("prompt for admin panel: %w", err)
		}
		if generate {
			opts.adminBridge = "admin-alpine"
		}
	}
	if opts.adminBridge != "" {
		adminManifest, err := generateAdminPanel(opts, irProject, seedData, mockDataset, log)
		if err != nil {
			return err
		}
		manifest = append(manifest, adminManifest...)
	}

	// --- Migration actions: clean up orphaned / renamed files ---
	// Files written from this run must never be touched by cleanup.
	fresh := make(map[string]bool)
	for _, f := range manifest {
		if f.Written {
			fresh[f.Path] = true
		}
	}
	applied, err := applyMigrationActions(opts, migrationDiff, fresh, log, cmd)
	if err != nil {
		return err
	}

	// --- Database migrations ---
	// Run the bridge's declared migration commands so the developer never
	// has to think about SQL schemas. Triggered explicitly via --migrate, or
	// automatically in --prune mode when the diff detected renames/deletions
	// that must reach the database (skipped when cleanup was deferred).
	autoMigrate := opts.prune && migrationDiff.HasSchemaChanges()
	if autoMigrate && chainHasCommunity {
		log.Warn("community bridge(s) in the resolved chain — not auto-running database migrations; use --migrate explicitly")
		autoMigrate = false
	}
	shouldMigrate := opts.migrate || autoMigrate
	cfg := rendererInstance.MigrationConfig()
	if applied && shouldMigrate && cfg != nil && cfg.Enabled {
		if !bridgeCommandsAllowed(chainHasCommunity, opts.allowBridgeCommands) {
			return fmt.Errorf("community bridge(s) declared database migrations; re-run with --allow-bridge-commands to execute them")
		}
		if err := runDatabaseMigrations(rendererInstance.MigrationConfig(), opts.outputDir, cmd, log); err != nil {
			if opts.migrate {
				// Explicit --migrate: a failed migration is a hard error.
				return err
			}
			// Auto-migration under --prune is best-effort. In a docker/CI
			// workflow the API container applies pending migrations at startup
			// (Database:SchemaMode=migrate → MigrateAsync), so a host without
			// the dotnet SDK or an unreachable database must not abort generation.
			log.Warn("Database migration was not applied by --prune: %v", err)
			log.Warn("In docker, set Database:SchemaMode=migrate — the container applies pending migrations on startup.")
		}
	}

	// --- Smart warning for type changes (manual refactoring report) ---
	if report := migrationDiff.TypeChangeReport(); report != "" {
		log.Warn("ACTION REQUIRED — manual refactoring may be needed")
		_, _ = fmt.Fprint(cmd.OutOrStdout(), report)
	}

	// --- Smart warning for field renames (data-preserving column rename) ---
	if report := migrationDiff.FieldRenameReport(); report != "" {
		log.Warn("ACTION REQUIRED — fields were renamed; apply a safe column rename to avoid data loss")
		_, _ = fmt.Fprint(cmd.OutOrStdout(), report)
	}

	// --- Project-rename warning: custom files with a stale root namespace ---
	if report := migrationDiff.NamespaceRenameReport(); report != "" {
		log.Warn("ACTION REQUIRED — project was renamed; custom files reference the old namespace")
		_, _ = fmt.Fprint(cmd.OutOrStdout(), report)
	}

	// --- Persist the new snapshot ---
	if !applied {
		// Cleanup was deferred (non-interactive without --prune). Keep the
		// previous snapshot so a later --prune run can still find the
		// orphaned/renamed files.
		log.Info("Snapshot kept — cleanup pending (re-run with --prune to finish)")
		return nil
	}
	bridgeID := opts.bridgePath
	if bridgeName != "" {
		bridgeID = bridgeName
	}
	// Record the resolved bridge chain next to domain.yaml (Phase 0.4).
	// Best-effort: a lock-write failure must never abort generation.
	if err := writeDomainLock(opts.domainFile, append([]string{opts.bridgePath}, basePaths...)); err != nil {
		log.Warn("could not write domaincraft.lock.json: %v", err)
	}
	if err := snapshot.Save(snapPath, snapshot.New(bridgeID, irProject, manifest)); err != nil {
		return fmt.Errorf("save schema snapshot: %w", err)
	}

	return nil
}

// --- bridges ---

func newBridgesCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridges",
		Short: "List available bridges",
		Long:  "Show all known bridges with their cache status.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			registry := bridge.Default()
			out := cmd.OutOrStdout()

			entries := registry.All()
			if len(entries) == 0 {
				_, _ = fmt.Fprintln(out, "No bridges registered.")
				return nil
			}

			_, _ = fmt.Fprintf(out, "%-20s %-10s %-18s %-16s %s\n", "ID", "Language", "Source", "Status", "Description")
			_, _ = fmt.Fprintf(out, "%-20s %-10s %-18s %-16s %s\n",
				strings.Repeat("-", 20),
				strings.Repeat("-", 10),
				strings.Repeat("-", 18),
				strings.Repeat("-", 16),
				strings.Repeat("-", 40),
			)

			for _, e := range entries {
				st := e.Status(opts.checkUpdates)
				source := "remote"
				switch {
				case st.Embedded && st.Cached:
					source = "embedded+cache"
				case st.Embedded:
					source = "embedded"
				case st.Cached:
					source = "cache"
				}
				status := ""
				switch {
				case st.Embedded:
					status = "bundled"
					if st.Outdated {
						status = "update available"
					}
				case st.Cached:
					status = "ready"
					if st.Outdated {
						status = "update available"
					}
				default:
					status = "not locally available"
				}
				if e.Deprecated {
					status += "+deprecated"
				}
				_, _ = fmt.Fprintf(out, "%-20s %-10s %-18s %-16s %s\n", e.ID, e.Language, source, status, e.Description)
			}

			_, _ = fmt.Fprintln(out, "\nLegend: embedded = inside this binary; cache = local copy; remote = fetched on demand.")
			_, _ = fmt.Fprintln(out, "Run `domaincraft generate --update-bridges` to refresh cached bridges to the latest supported version.")
			return nil
		},
	}

	// --check-updates — contact each cached bridge's remote and report whether a
	// newer version is available (no modification, just status).
	cmd.Flags().BoolVar(&opts.checkUpdates, "check-updates", false, "contact remotes and report whether cached bridges are outdated")

	return cmd
}

// --- helpers ---

// newBridgeResolver builds a resolver with the CLI's bridge cache/update options.
func newBridgeResolver(opts *cliOptions, log *logger.Logger) *bridge.Resolver {
	return bridge.NewResolver(bridge.Default()).WithEnsureOptions(bridgeEnsureOptions(opts, log))
}

// walkBridgeChain resolves ref relative to fromDir and follows `extends` to the
// deepest base. It returns absolute paths top-first: the resolved bridge first,
// then its bases. Absolute paths make cycle detection path-based, not textual.
func walkBridgeChain(opts *cliOptions, ref, fromDir string, log *logger.Logger) ([]string, error) {
	resolver := newBridgeResolver(opts, log)
	var chain []string
	visited := make(map[string]bool)
	for ref != "" {
		path, err := bridge.ResolveExtendsRef(ref, fromDir, resolver)
		if err != nil {
			return nil, fmt.Errorf("resolve bridge `extends: %s`: %w", ref, err)
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if visited[path] {
			return nil, fmt.Errorf("bridge `extends` cycle detected at %q", ref)
		}
		visited[path] = true
		chain = append(chain, path)

		base, err := renderer.New(path, log)
		if err != nil {
			return nil, fmt.Errorf("load bridge %s: %w", path, err)
		}
		ref, fromDir = base.Extends(), path
	}
	return chain, nil
}

// resolveBridgeInteractive resolves the bridge from the --bridge flag, or
// prompts the user interactively. Returns (path, displayName, error).
func resolveBridgeInteractive(opts *cliOptions, log *logger.Logger) (string, string, error) {
	resolver := newBridgeResolver(opts, log)

	if opts.bridgePath != "" {
		// Monorepo-first: an official bridge ID resolves to the local
		// <root>/bridges/<path> copy when it exists (same order as `extends`).
		if entry := bridge.Default().ByID(opts.bridgePath); entry != nil && entry.Path != "" {
			if wd, err := os.Getwd(); err == nil {
				if p := bridge.FindMonorepoBridge(wd, entry.Path); p != "" {
					return p, "", nil
				}
			}
		}
		resolved, err := resolver.Resolve(opts.bridgePath)
		return resolved, "", err
	}

	if opts.nonInteractive || !isTerminal() {
		return "", "", fmt.Errorf("--bridge is required in non-interactive mode")
	}

	entry, err := interactive.SelectBridge(bridge.Default())
	if err != nil {
		return "", "", err
	}

	resolved, err := resolver.Resolve(entry.ID)
	if err != nil {
		return "", "", fmt.Errorf("resolve bridge %q: %w", entry.ID, err)
	}

	return resolved, entry.Name, nil
}

// resolveBaseChain resolves an adapter bridge's `extends` chain to local bridge
// paths, ordered outermost base first (so the deepest base renders first). Each
// base is resolved the same way --bridge is: a path, registry ID or owner/repo.
func resolveBaseChain(opts *cliOptions, adapterDir, baseRef string, log *logger.Logger) ([]string, error) {
	chain, err := walkBridgeChain(opts, baseRef, adapterDir, log)
	if err != nil {
		return nil, err
	}
	// The walker returns the deepest chain top-first; bases must render with
	// the outermost base first, so reverse it.
	slices.Reverse(chain)
	return chain, nil
}

// replaceEntry is one --replace flag: left (= layer or bridge name) -> right (bridge ref).
type replaceEntry struct {
	left  string
	right string
}

func parseReplaceFlags(raw []string) ([]replaceEntry, error) {
	var out []replaceEntry
	for _, s := range raw {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("invalid --replace %q: expected layer|bridgeId=bridgeRef", s)
		}
		out = append(out, replaceEntry{left: strings.TrimSpace(parts[0]), right: strings.TrimSpace(parts[1])})
	}
	return out, nil
}

// bridgeLayerAndName loads bridge.yaml for dir and returns layer and name.
func bridgeLayerAndName(dir string, log *logger.Logger) (string, string, error) {
	r, err := renderer.New(dir, log)
	if err != nil {
		return "", "", err
	}
	cfg := r.Config()
	return cfg.Layer, cfg.Name, nil
}

// buildFullChainTopFirst builds the full chain top-first: [entry, base1, base2, ... deepest].
func buildFullChainTopFirst(opts *cliOptions, entryDir string, log *logger.Logger) ([]string, error) {
	return walkBridgeChain(opts, entryDir, entryDir, log)
}

// resolveChainWithReplacements resolves the extends chain with --replace overrides.
// It returns the (possibly replaced) entry dir and base paths outermost-first.
func resolveChainWithReplacements(opts *cliOptions, entryDir string, reps []replaceEntry, log *logger.Logger) (string, []string, error) {
	if len(reps) == 0 {
		r, err := renderer.New(entryDir, log)
		if err != nil {
			return "", nil, err
		}
		bases, err := resolveBaseChain(opts, entryDir, r.Extends(), log)
		return entryDir, bases, err
	}
	resolver := newBridgeResolver(opts, log)
	fullChain, err := buildFullChainTopFirst(opts, entryDir, log)
	if err != nil {
		return "", nil, err
	}
	for _, rep := range reps {
		// Find target index by layer or name.
		targetIdx := -1
		var targetLayer string
		for i, dir := range fullChain {
			layer, name, err := bridgeLayerAndName(dir, log)
			if err != nil {
				return "", nil, err
			}
			if layer == rep.left || name == rep.left {
				targetIdx = i
				targetLayer = layer
				break
			}
		}
		if targetIdx == -1 {
			return "", nil, fmt.Errorf("no bridge with layer or name %q in chain %v", rep.left, fullChain)
		}
		// Resolve replacement bridge.
		rightPath, err := bridge.ResolveExtendsRef(rep.right, entryDir, resolver)
		if err != nil {
			return "", nil, fmt.Errorf("resolve --replace %s=%s: %w", rep.left, rep.right, err)
		}
		if abs, err := filepath.Abs(rightPath); err == nil {
			rightPath = abs
		}
		rightLayer, rightName, err := bridgeLayerAndName(rightPath, log)
		if err != nil {
			return "", nil, err
		}
		// Warn on layer mismatch (both non-empty and different).
		if targetLayer != "" && rightLayer != "" && targetLayer != rightLayer && log != nil {
			log.Warn("layer mismatch: replacing %q (layer=%q) with %q (layer=%q)", rep.left, targetLayer, rightName, rightLayer)
		}
		// Build replacement's full chain top-first.
		rightChain, err := buildFullChainTopFirst(opts, rightPath, log)
		if err != nil {
			return "", nil, err
		}
		// Splice: fullChain[:targetIdx] + rightChain (replaces tail after targetIdx)
		newChain := make([]string, 0, len(fullChain[:targetIdx])+len(rightChain))
		newChain = append(newChain, fullChain[:targetIdx]...)
		newChain = append(newChain, rightChain...)
		// Validate no duplicate layer and no cycle.
		seenLayer := make(map[string]string)
		seenPath := make(map[string]bool)
		for _, d := range newChain {
			abs, _ := filepath.Abs(d)
			if seenPath[abs] {
				return "", nil, fmt.Errorf("bridge `extends` cycle detected at %q", d)
			}
			seenPath[abs] = true
			layer, _, err := bridgeLayerAndName(d, log)
			if err != nil {
				return "", nil, fmt.Errorf("read layer of %s: %w", d, err)
			}
			if layer != "" {
				if prev, ok := seenLayer[layer]; ok {
					return "", nil, fmt.Errorf("duplicate layer %q: %q and %q", layer, prev, d)
				}
				seenLayer[layer] = d
			}
		}
		fullChain = newChain
	}
	newEntry := fullChain[0]
	tail := fullChain[1:]
	slices.Reverse(tail)
	return newEntry, tail, nil
}

// --- domaincraft.lock.json (Phase 0.4) ---
//
// The lock records the exact bridge chain that rendered a project, next to the
// domain.yaml (not inside the output dir, not in the snapshot). It holds no
// timestamps, so it is diff-friendly and reproducible. It answers "which
// bridges rendered this project" — the snapshot answers "what did the model
// look like".

// lockBridge is one entry in domaincraft.lock.json.
type lockBridge struct {
	ID              string `json:"id"`
	Path            string `json:"path,omitempty"`   // monorepo path under bridges/ when resolved locally from the monorepo
	Source          string `json:"source,omitempty"` // GitHub owner/repo when resolved from the registry/cache
	Version         string `json:"version,omitempty"`
	ContractVersion int    `json:"contract_version"`
}

// domainLock is the on-disk shape of domaincraft.lock.json.
type domainLock struct {
	Bridges []lockBridge `json:"bridges"`
}

// writeDomainLock records the resolved bridge chain (top-first: entry, then its
// bases) next to domainPath. It is best-effort: a failure to write the lock
// must never break generation.
func writeDomainLock(domainPath string, dirs []string) error {
	reg := bridge.Default()
	var out []lockBridge
	seen := make(map[string]bool)
	for _, dir := range dirs {
		id, version, contractVersion, err := bridgeMetadata(dir)
		if err != nil {
			return err
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		b := lockBridge{ID: id, Version: version, ContractVersion: contractVersion}
		if entry := reg.ByID(id); entry != nil {
			if p := bridge.MonorepoRelPath(dir, entry.Path); p != "" {
				b.Path = p
			} else if entry.GitHub != "" {
				b.Source = entry.GitHub
			}
		}
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b lockBridge) int { return cmp.Compare(a.ID, b.ID) })

	data, err := json.MarshalIndent(domainLock{Bridges: out}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(domainPath)
	if dir == "" {
		dir = "."
	}
	return fsutil.AtomicWrite(filepath.Join(dir, "domaincraft.lock.json"), data)
}

// bridgeMetadata reads the identifying fields out of a bridge directory's
// bridge.yaml for the lock file.
func bridgeMetadata(dir string) (id, version string, contractVersion int, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "bridge.yaml")) //nolint:gosec // dir comes from validated bridge resolution
	if err != nil {
		return "", "", 0, err
	}
	var cfg struct {
		Name            string `yaml:"name"`
		Version         string `yaml:"version"`
		ContractVersion int    `yaml:"contract_version"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", "", 0, err
	}
	if cfg.ContractVersion == 0 {
		cfg.ContractVersion = 1
	}
	return cfg.Name, cfg.Version, cfg.ContractVersion, nil
}

// bridgeEnsureOptions builds the bridge caching/update policy from the CLI
// flags and terminal state:
//   - --update-bridges consents to downloading newer versions (CI-safe);
//   - otherwise, an interactive terminal is prompted when an update is found;
//   - in non-interactive mode the cached copy is kept and a warning is logged.
func bridgeEnsureOptions(cli *cliOptions, log *logger.Logger) *bridge.EnsureOptions {
	opts := &bridge.EnsureOptions{
		Force: cli.updateBridges,
		Warn:  log.Warn,
	}

	switch {
	case cli.updateBridges:
		// The flag itself is the consent — update without prompting.
		opts.ConfirmUpdate = func(entry bridge.RegistryEntry, u *bridge.Update) (bool, error) {
			log.Info("%s", bridgeUpdateNotice(entry.ID, u))
			return true, nil
		}
	case !cli.nonInteractive && isTerminal():
		opts.ConfirmUpdate = func(entry bridge.RegistryEntry, u *bridge.Update) (bool, error) {
			return interactive.PromptBridgeUpdate(entry, u)
		}
	default:
		// Non-interactive (even on a TTY) without the flag: keep the cached
		// copy; the bridge package logs a warning pointing at --update-bridges.
		opts.ConfirmUpdate = nil
	}
	return opts
}

// bridgeUpdateNotice renders the log line for a bridge update. It deliberately
// omits version numbers: bridge.Update only carries the cached (old) version,
// which must not be presented as the available release.
func bridgeUpdateNotice(entryID string, _ *bridge.Update) string {
	return fmt.Sprintf("Updating bridge %s (cached copy is outdated)", entryID)
}

// generateAdminPanel renders the optional admin panel bridge into the output
// directory and returns the extra file manifest entries.
func generateAdminPanel(opts *cliOptions, irProject *ir.IRProject, seedData, mockDataset *ir.SeedDataset, log *logger.Logger) ([]renderer.RenderedFile, error) {
	resolver := bridge.NewResolver(bridge.Default()).WithEnsureOptions(bridgeEnsureOptions(opts, log))

	adminID := opts.adminBridge
	if adminID == "" {
		adminID = "admin-alpine"
	}

	adminPath, err := resolver.Resolve(adminID)
	if err != nil {
		return nil, fmt.Errorf("resolve admin bridge %q: %w", adminID, err)
	}

	log.Info("Rendering admin panel via %s", adminID)
	adminRenderer, err := renderer.New(adminPath, log)
	if err != nil {
		return nil, err
	}
	adminRenderer.SetSeedData(seedData)
	adminRenderer.SetMockData(mockDataset)

	adminFiles, adminManifest, err := adminRenderer.Render(irProject, opts.outputDir)
	if err != nil {
		return nil, err
	}

	log.Success("Generated %d admin file(s)", len(adminFiles))
	return adminManifest, nil
}

// bridgeCommandsAllowed reports whether shell lines from a community bridge
// may run. Official bundled bridges are trusted; community bridges need the
// explicit --allow-bridge-commands consent.
func bridgeCommandsAllowed(chainHasCommunity, allow bool) bool {
	return !chainHasCommunity || allow
}

// runDatabaseMigrations executes the bridge's declared database-migration
// commands in order, from the generated output directory. Commands are shell
// lines invoked through the platform shell so bridges can rely on standard
// tooling (e.g. `dotnet ef ...`). A failing command aborts the run.
func runDatabaseMigrations(cfg *renderer.MigrationConfig, outDir string, cmd *cobra.Command, log *logger.Logger) error {
	if cfg == nil || !cfg.Enabled || len(cfg.Commands) == 0 {
		return nil
	}
	out := cmd.OutOrStdout()
	// The resolved command lines are printed before they run, so the developer
	// always sees exactly what will be executed (Phase 3.5 safety).
	_, _ = fmt.Fprintf(out, "  Applying %d database migration command(s):\n", len(cfg.Commands))
	for i, command := range cfg.Commands {
		_, _ = fmt.Fprintf(out, "    %d) %s\n", i+1, command)
	}
	for i, command := range cfg.Commands {
		log.Info("Migration %d/%d: %s", i+1, len(cfg.Commands), command)
		var argv []string
		var shell string
		if runtime.GOOS == "windows" {
			shell = "cmd"
			argv = []string{"/C", command}
		} else {
			shell = "sh"
			argv = []string{"-c", command}
		}
		c := exec.CommandContext(commandCtx, shell, argv...) //nolint:gosec // shell line is gated by explicit bridge-command consent
		c.Dir = outDir
		c.Stdout = out
		c.Stderr = out
		if err := c.Run(); err != nil {
			return fmt.Errorf("database migration %d failed: %w", i+1, err)
		}
	}
	log.Success("Database migrations applied")
	return nil
}

// applyMigrationActions handles orphaned file cleanup for entities that were
// removed or renamed since the last generation. In interactive mode the user
// is prompted before touching custom (overwrite: false) files. With --prune
// everything is done automatically; otherwise (non-interactive) a warning is
// printed and nothing is modified. Files freshly written this run (fresh) are
// never touched.
//
// It returns whether all actions were applied. When cleanup was deferred
// (non-interactive without --prune) it returns false so the caller keeps the
// previous snapshot — otherwise orphaned files would be forgotten and a later
// --prune run could no longer find them.
func applyMigrationActions(opts *cliOptions, diff *snapshot.Diff, fresh map[string]bool, log *logger.Logger, cmd *cobra.Command) (bool, error) {
	if diff == nil || diff.IsEmpty() {
		return true, nil
	}
	out := cmd.OutOrStdout()

	// In --prune mode the tool owns the whole cleanup, so it also rewrites
	// identifier references in developer-owned files: the project's root
	// namespace after a project rename, and renamed entity fields. Generated
	// files are already regenerated with the new names; custom files are not,
	// so this is what keeps them compiling.
	if opts.prune {
		if diff.NamespaceRename != nil {
			for _, rel := range diff.NamespaceRename.Files {
				if fresh[rel] {
					continue
				}
				rewrote, err := snapshot.RewriteFile(opts.outputDir, rel, snapshot.NamespaceTransform(diff.NamespaceRename))
				if err != nil {
					log.Warn("could not rewrite namespace in %s: %v", rel, err)
					continue
				}
				if rewrote {
					_, _ = fmt.Fprintf(out, "  ▸ updated namespace %s -> %s in %s\n", diff.NamespaceRename.OldNamespace, diff.NamespaceRename.NewNamespace, rel)
				}
			}
		}
		for _, fr := range diff.FieldRenames {
			for _, rel := range fr.CustomFiles {
				if fresh[rel] {
					continue
				}
				rewrote, err := snapshot.RewriteFile(opts.outputDir, rel, snapshot.FieldTransform(fr))
				if err != nil {
					log.Warn("could not rewrite field references in %s: %v", rel, err)
					continue
				}
				if rewrote {
					_, _ = fmt.Fprintf(out, "  ▸ updated field %s -> %s in %s\n", fr.OldField, fr.NewField, rel)
				}
			}
		}
	}

	applied := true
	for _, del := range diff.Deleted {
		if len(del.Files) == 0 {
			continue
		}
		done, err := handleDeletedEntity(opts, del, fresh, log, out)
		if err != nil {
			return false, err
		}
		applied = applied && done
	}

	for _, ren := range diff.Renamed {
		if len(ren.Files) == 0 {
			continue
		}
		done, err := handleRename(opts, ren, diff, fresh, log, out)
		if err != nil {
			return false, err
		}
		applied = applied && done
	}
	return applied, nil
}

// chooseMigrationFiles returns the files selected for cleanup. --prune takes
// all of them; otherwise the caller's prompt decides. The non-interactive
// fallback stays in the caller because its warning is operation-specific.
func chooseMigrationFiles(opts *cliOptions, files []snapshot.FileRef, prompt func([]interactive.FileChoice) ([]string, error)) (map[string]bool, error) {
	selected := make(map[string]bool)
	if opts.prune {
		for _, f := range files {
			selected[f.Path] = true
		}
		return selected, nil
	}
	choices := make([]interactive.FileChoice, len(files))
	for i, f := range files {
		choices[i] = interactive.FileChoice{Path: f.Path, Label: fileLabel(f)}
	}
	picked, err := prompt(choices)
	if err != nil {
		return nil, err
	}
	for _, p := range picked {
		selected[p] = true
	}
	return selected, nil
}

// handleDeletedEntity deletes generated files automatically and asks (or warns)
// about custom files of a removed entity. Returns false when cleanup was
// deferred (non-interactive without --prune).
func handleDeletedEntity(opts *cliOptions, del snapshot.DeletedEntity, fresh map[string]bool, log *logger.Logger, out io.Writer) (bool, error) {
	if !opts.prune && (opts.nonInteractive || !isTerminal()) {
		log.Warn("Entity %q was removed from domain.yaml — orphaned files kept (re-run with --prune to delete):", del.Name)
		for _, f := range del.Files {
			_, _ = fmt.Fprintf(out, "    - %s%s\n", f.Path, customMark(f))
		}
		return false, nil
	}
	selected, err := chooseMigrationFiles(opts, del.Files, func(choices []interactive.FileChoice) ([]string, error) {
		return interactive.PromptDeleteFiles(del.Name, choices)
	})
	if err != nil {
		return false, err
	}

	for _, f := range del.Files {
		if !selected[f.Path] || fresh[f.Path] {
			continue
		}
		if err := snapshot.DeleteFile(opts.outputDir, f.Path); err != nil {
			log.Warn("could not delete %s: %v", f.Path, err)
			continue
		}
		_, _ = fmt.Fprintf(out, "  ▸ deleted %s\n", f.Path)
	}
	return true, nil
}

// handleRename renames custom files after a prompt and removes stale generated
// files (they have been regenerated under the new entity name). In --prune mode
// the renamed files' contents are rewritten too (entity, namespace and field
// identifiers), so the developer-owned file keeps compiling after the rename.
// Returns false when cleanup was deferred (non-interactive without --prune).
func handleRename(opts *cliOptions, ren snapshot.Rename, d *snapshot.Diff, fresh map[string]bool, log *logger.Logger, out io.Writer) (bool, error) {
	if !opts.prune && (opts.nonInteractive || !isTerminal()) {
		log.Warn("Entity %q was renamed to %q — files kept (re-run with --prune to rename):", ren.OldName, ren.NewName)
		for _, f := range ren.Files {
			_, _ = fmt.Fprintf(out, "    - %s -> %s\n", f.Path, snapshot.RenameRelPath(f.Path, ren.OldName, ren.NewName))
		}
		return false, nil
	}
	selected, err := chooseMigrationFiles(opts, ren.Files, func(choices []interactive.FileChoice) ([]string, error) {
		return interactive.PromptRenameFiles(ren.OldName, ren.NewName, choices)
	})
	if err != nil {
		return false, err
	}

	for _, f := range ren.Files {
		if !selected[f.Path] || fresh[f.Path] {
			continue
		}
		if f.Custom {
			newRel := snapshot.RenameRelPath(f.Path, ren.OldName, ren.NewName)
			if fresh[newRel] {
				// The destination was scaffolded this run by the custom
				// (overwrite: false) template. Replace it with the developer's
				// existing custom file so custom hooks survive the rename.
				if err := os.Remove(filepath.Join(opts.outputDir, filepath.FromSlash(newRel))); err != nil {
					log.Warn("could not replace scaffolded %s: %v", newRel, err)
					continue
				}
			}
			newRel, renamed, err := snapshot.RenameEntityFile(opts.outputDir, f.Path, ren.OldName, ren.NewName)
			if err != nil {
				log.Warn("could not rename %s: %v", f.Path, err)
				continue
			}
			if renamed {
				if rewrote, rerr := snapshot.RewriteFile(opts.outputDir, newRel, snapshot.RenameTransforms(ren.OldName, ren.NewName, d)...); rerr != nil {
					log.Warn("could not rewrite content of %s: %v", newRel, rerr)
				} else if rewrote {
					_, _ = fmt.Fprintf(out, "  ▸ renamed %s -> %s (content updated)\n", f.Path, newRel)
					continue
				}
				_, _ = fmt.Fprintf(out, "  ▸ renamed %s -> %s\n", f.Path, newRel)
			}
			continue
		}
		// Generated files have been regenerated under the new name — remove the stale copy.
		if err := snapshot.DeleteFile(opts.outputDir, f.Path); err != nil {
			log.Warn("could not delete stale generated file %s: %v", f.Path, err)
			continue
		}
		_, _ = fmt.Fprintf(out, "  ▸ removed stale generated file %s\n", f.Path)
	}
	return true, nil
}

// fileLabel renders a file choice label, annotating developer-owned files.
func fileLabel(f snapshot.FileRef) string {
	return f.Path + customMark(f)
}

// customMark returns a marker suffix for custom (overwrite: false) files.
func customMark(f snapshot.FileRef) string {
	if f.Custom {
		return "  [custom]"
	}
	return ""
}

// applyAddons parses the --addons flag into the IR project's Addons set,
// validating each name against specmeta. Unknown addons are silently ignored so
// bridges that don't know a given addon simply render their non-addon templates.
func applyAddons(opts *cliOptions, project *ir.IRProject) {
	if strings.TrimSpace(opts.addons) == "" {
		return
	}
	if project.Addons == nil {
		project.Addons = make(map[string]bool)
	}
	for addon := range strings.SplitSeq(opts.addons, ",") {
		addon = strings.TrimSpace(addon)
		if addon == "" {
			continue
		}
		if specmeta.IsAddon(addon) {
			project.Addons[addon] = true
		}
	}
}

// loadAndValidate reports warnings on the command's error stream and returns
// hard validation errors as one aggregated error. The caller prints that error
// once; printing each error here as well would duplicate every line.
func loadAndValidate(opts *cliOptions, cmd *cobra.Command) (*parser.ParsedSchema, error) {
	data, err := os.ReadFile(opts.domainFile)
	if err != nil {
		return nil, fmt.Errorf("read domain file: %w", err)
	}
	schema, err := parser.ParseYAML(data)
	if err != nil {
		return nil, err
	}

	errOut := cmd.ErrOrStderr()
	hardErrors := make([]error, 0)
	for _, e := range validator.New(schema).Validate() {
		if e.Warning {
			_, _ = fmt.Fprintf(errOut, "⚠ %s\n", e.Error())
			continue
		}
		hardErrors = append(hardErrors, e)
	}
	if len(hardErrors) > 0 {
		return nil, fmt.Errorf("validation failed with %d error(s): %w", len(hardErrors), errors.Join(hardErrors...))
	}

	return schema, nil
}
