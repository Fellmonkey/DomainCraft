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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/bridgechain"
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

	// databaseMigrationTimeout bounds one batch of bridge-declared database
	// migration commands. The signal context from main stays the parent, so an
	// interrupt still cancels a child immediately; this only stops a hung
	// command from blocking a generation run forever.
	databaseMigrationTimeout = 10 * time.Minute
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
	// isTerminal is a seam for testing prompt gating without a real TTY.
	isTerminal = interactive.IsTerminal

	// execCommandFn constructs child processes for bridge-declared database
	// migrations. It is a seam so tests can inspect the working directory and
	// context passed to a command without changing production behavior.
	execCommandFn = exec.CommandContext

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

// errUsage marks CLI misuse: bad flags, bad arguments or a missing required
// flag. main maps it to exit code 2; every other error is a runtime failure
// (exit 1). The message stays that of the underlying error — the marker is
// invisible to the user and to log output.
var errUsage = errors.New("usage error")

type usageError struct{ err error }

// Error returns the underlying CLI misuse message.
func (u usageError) Error() string { return u.err.Error() }

// Unwrap exposes the original error for errors.Is/As inspection.
func (u usageError) Unwrap() error { return u.err }

// Is marks the error as CLI misuse so main can map it to exit code 2.
func (u usageError) Is(target error) bool {
	return target == errUsage
}

// asUsageError tags an error raised inside RunE so it gets the usage exit code.
func asUsageError(err error) error {
	if err == nil {
		return nil
	}
	return usageError{err: err}
}

// usageOnInvalidBridgeRef maps a rejected bridge reference (a bad flag value)
// to a usage error, while leaving clone/network failures as runtime errors.
func usageOnInvalidBridgeRef(err error) error {
	if err == nil || !errors.Is(err, bridge.ErrInvalidRef) {
		return err
	}
	return asUsageError(err)
}

// newRootCommandFn is a seam so tests can attach their own error writer to
// the command tree instead of replacing the process-global os.Stderr.
var newRootCommandFn = newRootCommand

// reportCommandError is the only place a command failure reaches stderr. A
// cleanup handler must return its error instead of also logging a warning, or
// the same failure would be printed twice.
func reportCommandError(w io.Writer, err error) {
	_, _ = fmt.Fprintln(w, "Error:", err)
}

// Execute runs the root command. Cobra is silenced so the error is printed
// exactly once here; the error is returned to main, which owns the exit code.
func Execute() error {
	return executeRoot(newRootCommandFn())
}

// executeRoot runs a prepared command tree. Errors raised before the command's
// PersistentPreRun (unknown flag, unknown command, failed argument validation)
// are usage errors by construction.
func executeRoot(root *cobra.Command) error {
	preRun := root.PersistentPreRun
	reachedRun := false
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		reachedRun = true
		if preRun != nil {
			preRun(cmd, args)
		}
	}
	if err := root.Execute(); err != nil {
		reportCommandError(root.ErrOrStderr(), err)
		if !reachedRun || errors.Is(err, errUsage) {
			return asUsageError(err)
		}
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

// loadAndValidateDomain parses and validates domain.yaml, then builds the IR
// and applies addons. resolveBridge is called between validation and IR
// building so prompts, log lines and errors keep their original order.
func loadAndValidateDomain(opts *cliOptions, cmd *cobra.Command, log *logger.Logger, resolveBridge func() (string, error)) (*ir.Project, string, error) {
	log.Info("Parsing %s", opts.domainFile)
	schema, err := loadAndValidate(opts, cmd)
	if err != nil {
		return nil, "", err
	}
	log.Success("Schema valid (%d entities)", len(schema.Entities))

	bridgeName, err := resolveBridge()
	if err != nil {
		return nil, "", err
	}

	log.Info("Building IR")
	irProject, err := ir.Build(schema)
	if err != nil {
		return nil, "", err
	}
	applyAddons(opts, irProject, log)
	return irProject, bridgeName, nil
}

// runGenerate is shared by `generate` and `init --bridge` so both paths go
// through the same validation, rendering and migration pipeline.
func runGenerate(opts *cliOptions, cmd *cobra.Command, args []string) error {
	log := commandLogger(cmd)
	ctx := &commandContext{opts: opts, cmd: cmd, log: log}

	irProject, bridgeName, err := loadAndValidateDomain(opts, cmd, log, func() (string, error) {
		resolvedPath, name, err := resolveBridgeInteractive(opts, log)
		if err != nil {
			return "", err
		}
		opts.bridgePath = resolvedPath
		return name, nil
	})
	if err != nil {
		return err
	}

	// --- Schema snapshot / migration diff ---
	snapPath := snapshot.Path(opts.outputDir)
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
		return asUsageError(err)
	}
	// Resolve extends chain with optional node replacements.
	newBridgePath, basePaths, err := resolveChainWithReplacements(opts, opts.bridgePath, replacements, log)
	if err != nil {
		return err
	}
	opts.bridgePath = newBridgePath
	// One resolved chain drives both the trust decision and the lock file.
	chainPaths := append([]string{opts.bridgePath}, basePaths...)
	// Phase 3.5: a community bridge (not a bundled official bridge) must
	// never have migrations auto-run — that would execute arbitrary shell
	// lines from an untrusted bridge.yaml during a code-generation run.
	chainHasCommunity := false
	for _, p := range chainPaths {
		if !bridge.IsOfficialBundled(p) {
			chainHasCommunity = true
			break
		}
	}
	log.Info("Rendering via %s", opts.bridgePath)
	writtenFiles, manifest, rendererInstance, err := renderBridge(irProject, renderOptions{
		path:        opts.bridgePath,
		outDir:      opts.outputDir,
		bases:       basePaths,
		plan:        migrationPlan,
		seedData:    seedData,
		mockDataset: mockDataset,
		log:         log,
	})
	if err != nil {
		return err
	}

	log.Success("Generated %d file(s) into %s", len(writtenFiles), opts.outputDir)

	// --- Admin panel generation ---
	adminManifest, err := generateAdminPanel(ctx, args, irProject, seedData, mockDataset)
	if err != nil {
		return err
	}
	manifest = append(manifest, adminManifest...)

	// --- Migration actions: clean up orphaned / renamed files ---
	applied, err := applySchemaMigrations(ctx, migrationDiff, manifest)
	if err != nil {
		return err
	}

	// --- Database migrations ---
	ctx.chainHasCommunity = chainHasCommunity
	ctx.cleanupApplied = applied
	if err := runBridgeDatabaseMigrations(ctx, migrationDiff, rendererInstance.MigrationConfig()); err != nil {
		return err
	}

	// --- Smart refactoring reports ---
	// The warning goes to stderr; the multi-line report is command data and
	// stays on stdout so it can be piped. Order is fixed for deterministic runs.
	for _, r := range []struct {
		report  func() string
		warning string
	}{
		{migrationDiff.TypeChangeReport, "ACTION REQUIRED — manual refactoring may be needed"},
		{migrationDiff.FieldRenameReport, "ACTION REQUIRED — fields were renamed; apply a safe column rename to avoid data loss"},
		{migrationDiff.NamespaceRenameReport, "ACTION REQUIRED — project was renamed; custom files reference the old namespace"},
	} {
		if report := r.report(); report != "" {
			log.Warn("%s", r.warning)
			_, _ = fmt.Fprint(cmd.OutOrStdout(), report)
		}
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
	if err := writeDomainLock(opts.domainFile, chainPaths); err != nil {
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

			// --check-updates probes every cached bridge over HTTP. Run the
			// probes with bounded concurrency, then print in registry order so
			// the output stays deterministic.
			type statusRow struct {
				entry  bridge.RegistryEntry
				status bridge.Status
			}
			rows := make([]statusRow, len(entries))
			if opts.checkUpdates {
				const maxConcurrentProbes = 4
				sem := make(chan struct{}, maxConcurrentProbes)
				var wg sync.WaitGroup
				for i, e := range entries {
					wg.Add(1)
					go func(i int, e bridge.RegistryEntry) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()
						rows[i] = statusRow{e, e.Status(true)}
					}(i, e)
				}
				wg.Wait()
			} else {
				for i, e := range entries {
					rows[i] = statusRow{e, e.Status(false)}
				}
			}

			for _, row := range rows {
				e, st := row.entry, row.status
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

// commandContext groups the state threaded through the generation pipeline's
// helper steps: parsed flags, the command (for stdio), its logger, and the two
// chain/cleanup facts the migration helpers act on.
type commandContext struct {
	opts              *cliOptions
	cmd               *cobra.Command
	log               *logger.Logger
	chainHasCommunity bool
	cleanupApplied    bool
}

// newBridgeResolver builds a resolver with the CLI's bridge cache/update options.
func newBridgeResolver(opts *cliOptions, log *logger.Logger) *bridge.Resolver {
	return bridge.NewResolver(bridge.Default()).WithEnsureOptions(bridgeEnsureOptions(opts, log))
}

// resolveBridgeInteractive resolves the bridge from the --bridge flag, or
// prompts the user interactively. Returns (path, displayName, error).
func resolveBridgeInteractive(opts *cliOptions, log *logger.Logger) (string, string, error) {
	resolver := newBridgeResolver(opts, log)

	if opts.bridgePath != "" {
		// Monorepo-first, through the same step `extends` uses.
		if wd, err := os.Getwd(); err == nil {
			if p := bridge.ResolveOfficialLocal(opts.bridgePath, wd); p != "" {
				return p, "", nil
			}
		}
		resolved, err := resolver.Resolve(opts.bridgePath)
		return resolved, "", usageOnInvalidBridgeRef(err)
	}

	if opts.nonInteractive || !isTerminal() {
		return "", "", asUsageError(fmt.Errorf("--bridge is required in non-interactive mode"))
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
	chain, err := bridgechain.Walk(baseRef, adapterDir, newBridgeResolver(opts, log), log)
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


// resolveChainWithReplacements resolves the extends chain with --replace overrides.
// It returns the (possibly replaced) entry dir and base paths outermost-first.
func resolveChainWithReplacements(opts *cliOptions, entryDir string, reps []replaceEntry, log *logger.Logger) (string, []string, error) {
	// One read per directory. A chain with N --replace flags used to parse
	// every manifest N+1 times through renderer.New and a second YAML reader.
	metaCache := make(map[string]bridgeManifest)
	metaFor := func(dir string) (bridgeManifest, error) {
		if m, ok := metaCache[dir]; ok {
			return m, nil
		}
		m, err := readBridgeManifest(dir)
		if err != nil {
			return bridgeManifest{}, err
		}
		metaCache[dir] = m
		return m, nil
	}
	if len(reps) == 0 {
		m, err := metaFor(entryDir)
		if err != nil {
			return "", nil, err
		}
		bases, err := resolveBaseChain(opts, entryDir, m.Extends, log)
		return entryDir, bases, err
	}
	resolver := newBridgeResolver(opts, log)
	fullChain, err := bridgechain.Walk(entryDir, entryDir, newBridgeResolver(opts, log), log)
	if err != nil {
		return "", nil, err
	}
	for _, rep := range reps {
		// Find target index by layer or name.
		targetIdx := -1
		var targetLayer string
		for i, dir := range fullChain {
			m, err := metaFor(dir)
			if err != nil {
				return "", nil, err
			}
			if m.Layer == rep.left || m.Name == rep.left {
				targetIdx = i
				targetLayer = m.Layer
				break
			}
		}
		if targetIdx == -1 {
			return "", nil, asUsageError(fmt.Errorf("no bridge with layer or name %q in chain %v", rep.left, fullChain))
		}
		// Resolve replacement bridge.
		rightPath, err := bridge.ResolveExtendsRef(rep.right, entryDir, resolver)
		if err != nil {
			return "", nil, asUsageError(fmt.Errorf("resolve --replace %s=%s: %w", rep.left, rep.right, err))
		}
		if abs, err := filepath.Abs(rightPath); err == nil {
			rightPath = abs
		}
		rightMeta, err := metaFor(rightPath)
		if err != nil {
			return "", nil, err
		}
		// Warn on layer mismatch (both non-empty and different).
		if targetLayer != "" && rightMeta.Layer != "" && targetLayer != rightMeta.Layer && log != nil {
			log.Warn("layer mismatch: replacing %q (layer=%q) with %q (layer=%q)", rep.left, targetLayer, rightMeta.Name, rightMeta.Layer)
		}
		// Build replacement's full chain top-first.
		rightChain, err := bridgechain.Walk(rightPath, rightPath, newBridgeResolver(opts, log), log)
		if err != nil {
			return "", nil, err
		}
		// Splice: fullChain[:targetIdx] + rightChain (replaces tail after targetIdx)
		newChain := make([]string, 0, len(fullChain[:targetIdx])+len(rightChain))
		newChain = append(newChain, fullChain[:targetIdx]...)
		newChain = append(newChain, rightChain...)
		// Validate no duplicate layer and no cycle.
		seenLayer := make(map[string]string)
		seenPaths := make([]string, 0, len(newChain))
		for _, d := range newChain {
			abs, err := filepath.Abs(d)
			if err != nil {
				return "", nil, fmt.Errorf("resolve absolute bridge path %q: %w", d, err)
			}
			if slices.ContainsFunc(seenPaths, func(seen string) bool { return bridge.SamePath(seen, abs) }) {
				return "", nil, asUsageError(fmt.Errorf("bridge `extends` cycle detected at %q", d))
			}
			seenPaths = append(seenPaths, abs)
			m, err := metaFor(d)
			if err != nil {
				return "", nil, fmt.Errorf("read layer of %s: %w", d, err)
			}
			if m.Layer != "" {
				if prev, ok := seenLayer[m.Layer]; ok {
					return "", nil, asUsageError(fmt.Errorf("duplicate layer %q: %q and %q", m.Layer, prev, d))
				}
				seenLayer[m.Layer] = d
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

	return fsutil.WriteJSON(filepath.Join(filepath.Dir(domainPath), "domaincraft.lock.json"), domainLock{Bridges: out})
}

// bridgeManifest is the identifying part of bridge.yaml. One reader serves
// --replace matching and the lock file, so defaults and contract parsing
// cannot diverge between the two consumers.
type bridgeManifest struct {
	Layer           string
	Name            string
	Version         string
	Extends         string
	ContractVersion int
}

// readBridgeManifest reads bridge.yaml once per directory.
func readBridgeManifest(dir string) (bridgeManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bridge.yaml")) //nolint:gosec // dir comes from validated bridge resolution
	if err != nil {
		return bridgeManifest{}, err
	}
	var cfg struct {
		Name    string `yaml:"name"`
		Layer   string `yaml:"layer"`
		Version string `yaml:"version"`
		Extends string `yaml:"extends"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return bridgeManifest{}, err
	}
	contractVersion, _, err := bridge.ParseContractVersion(data)
	if err != nil {
		return bridgeManifest{}, err
	}
	return bridgeManifest{
		Layer: cfg.Layer, Name: cfg.Name, Version: cfg.Version,
		Extends: cfg.Extends, ContractVersion: contractVersion,
	}, nil
}

// bridgeMetadata is the lock-file view of the shared manifest reader.
func bridgeMetadata(dir string) (string, string, int, error) {
	m, err := readBridgeManifest(dir)
	if err != nil {
		return "", "", 0, err
	}
	return m.Name, m.Version, m.ContractVersion, nil
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

// runBridgeDatabaseMigrations applies the bridge's declared schema commands.
// They run with --migrate, or automatically under --prune when the diff has
// changes that must reach the database. Community bridges never auto-run them;
// explicit --migrate still needs --allow-bridge-commands consent. A failure is
// hard with --migrate and best-effort under --prune (the container can apply
// pending migrations at startup).
func runBridgeDatabaseMigrations(ctx *commandContext, diff *snapshot.Diff, cfg *renderer.MigrationConfig) error {
	autoMigrate := ctx.opts.prune && diff.HasSchemaChanges()
	if autoMigrate && ctx.chainHasCommunity {
		ctx.log.Warn("community bridge(s) in the resolved chain — not auto-running database migrations; use --migrate explicitly")
		autoMigrate = false
	}
	shouldMigrate := ctx.opts.migrate || autoMigrate
	if !ctx.cleanupApplied || !shouldMigrate || cfg == nil || !cfg.Enabled {
		return nil
	}
	if !bridgeCommandsAllowed(ctx.chainHasCommunity, ctx.opts.allowBridgeCommands) {
		return fmt.Errorf("community bridge(s) declared database migrations; re-run with --allow-bridge-commands to execute them")
	}
	if err := runDatabaseMigrations(cfg, ctx.opts.outputDir, ctx.cmd, ctx.log); err != nil {
		if ctx.opts.migrate {
			// Explicit --migrate: a failed migration is a hard error.
			return err
		}
		// Auto-migration under --prune is best-effort. In a docker/CI
		// workflow the API container applies pending migrations at startup
		// (Database:SchemaMode=migrate → MigrateAsync), so a host without
		// the dotnet SDK or an unreachable database must not abort generation.
		ctx.log.Warn("Database migration was not applied by --prune: %v", err)
		ctx.log.Warn("In docker, set Database:SchemaMode=migrate — the container applies pending migrations on startup.")
	}
	return nil
}

// applySchemaMigrations cleans up files orphaned by removed or renamed
// entities. Files written by this run are never touched. It reports whether
// all actions were applied: deferred cleanup keeps the previous snapshot.
func applySchemaMigrations(ctx *commandContext, diff *snapshot.Diff, manifest []renderer.RenderedFile) (bool, error) {
	// Files written from this run must never be touched by cleanup.
	fresh := make(map[string]bool)
	for _, f := range manifest {
		if f.Written {
			fresh[f.Path] = true
		}
	}
	return applyMigrationActions(ctx.opts, diff, fresh, ctx.log, ctx.cmd)
}

// generateAdminPanel handles the optional --admin flag and renders the panel
// into the output directory. It returns the extra file manifest entries; an
// empty manifest means no panel was requested.
func generateAdminPanel(ctx *commandContext, args []string, irProject *ir.Project, seedData, mockDataset *ir.SeedDataset) ([]renderer.RenderedFile, error) {
	// pflag's NoOptDefVal makes `--admin <id>` stop consuming the next
	// argument (it lands in args), so reclaim it here. This keeps both
	// `--admin <path>` and `--admin=<path>` working while `--admin` alone
	// still means the default admin-alpine bridge. Any other positional
	// argument is rejected by cobra.MaximumNArgs(1) / the check below.
	if len(args) > 0 {
		if !ctx.cmd.Flags().Changed("admin") || ctx.opts.adminBridge != "admin-alpine" {
			return nil, asUsageError(fmt.Errorf("unexpected positional argument %q: use --admin <bridge-id>", args[0]))
		}
		ctx.opts.adminBridge = args[0]
	}
	if ctx.opts.adminBridge == "" && !ctx.cmd.Flags().Changed("admin") && !ctx.opts.nonInteractive && isTerminal() {
		generate, err := interactive.PromptGenerateAdmin()
		if err != nil {
			return nil, fmt.Errorf("prompt for admin panel: %w", err)
		}
		if generate {
			ctx.opts.adminBridge = "admin-alpine"
		}
	}
	if ctx.opts.adminBridge == "" {
		return nil, nil
	}

	resolver := newBridgeResolver(ctx.opts, ctx.log)
	adminID := ctx.opts.adminBridge

	adminPath, err := resolver.Resolve(adminID)
	if err != nil {
		return nil, usageOnInvalidBridgeRef(fmt.Errorf("resolve admin bridge %q: %w", adminID, err))
	}

	ctx.log.Info("Rendering admin panel via %s", adminID)
	// The admin bridge has no extends chain of its own and no schema-migration
	// plan, so both stay explicitly empty. Success wording stays admin-specific.
	adminFiles, adminManifest, _, err := renderBridge(irProject, renderOptions{
		path:        adminPath,
		outDir:      ctx.opts.outputDir,
		bases:       nil,
		plan:        nil,
		seedData:    seedData,
		mockDataset: mockDataset,
		log:         ctx.log,
	})
	if err != nil {
		return nil, err
	}

	ctx.log.Success("Generated %d admin file(s)", len(adminFiles))
	return adminManifest, nil
}

// renderOptions groups the renderer inputs that differ between the generate and
// admin paths. Empty Bases/Plan is explicit for the admin bridge, which has no
// extends chain and no schema-migration plan.
type renderOptions struct {
	path        string
	outDir      string
	bases       []string
	plan        *ir.MigrationPlan
	seedData    *ir.SeedDataset
	mockDataset *ir.SeedDataset
	log         *logger.Logger
}

// renderBridge performs the renderer setup shared by `generate` and the admin
// panel: New → AttachBases → SetMigration → SetSeedData → SetMockData → Render.
// The renderer is returned so the caller can read its declared migration config.
// Success logging stays with the callers because the two paths use different wording.
func renderBridge(irProject *ir.Project, o renderOptions) ([]string, []renderer.RenderedFile, *renderer.Renderer, error) {
	r, err := renderer.New(o.path, o.log)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := r.AttachBases(o.bases); err != nil {
		return nil, nil, nil, err
	}
	r.SetMigration(o.plan)
	r.SetSeedData(o.seedData)
	r.SetMockData(o.mockDataset)

	files, manifest, err := r.Render(irProject, o.outDir)
	if err != nil {
		return nil, nil, nil, err
	}
	return files, manifest, r, nil
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
	// Migration progress and child-process output are diagnostics, not
	// machine-readable data: stdout stays clean for piping.
	out := cmd.ErrOrStderr()
	// The resolved command lines are printed before they run, so the developer
	// always sees exactly what will be executed (Phase 3.5 safety). Bridge YAML
	// is untrusted input: strip terminal controls before showing or logging a
	// command. Execution still uses the original line, unchanged.
	_, _ = fmt.Fprintf(out, "  Applying %d database migration command(s):\n", len(cfg.Commands))
	for i, command := range cfg.Commands {
		_, _ = fmt.Fprintf(out, "    %d) %s\n", i+1, sanitizeCommandLine(command))
	}

	// The signal context from main stays the parent: an interrupt still
	// cancels the child at once. Add a deadline so a hung bridge command
	// cannot block a generation run forever. An already-finished parent is
	// passed through unchanged — no child starts anyway, and callers can
	// observe the exact cancellation they installed.
	parent := bridge.CommandContext()
	migrationCtx := parent
	if parent.Err() == nil {
		var cancel context.CancelFunc
		migrationCtx, cancel = context.WithTimeout(parent, databaseMigrationTimeout)
		defer cancel()
	}

	for i, command := range cfg.Commands {
		log.Info("Migration %d/%d", i+1, len(cfg.Commands))
		var argv []string
		var shell string
		if runtime.GOOS == "windows" {
			shell = "cmd"
			argv = []string{"/C", command}
		} else {
			shell = "sh"
			argv = []string{"-c", command}
		}
		c := execCommandFn(migrationCtx, shell, argv...)
		c.Dir = outDir
		c.Stdout = out
		c.Stderr = out
		if err := c.Run(); err != nil {
			if errors.Is(migrationCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("database migration %d (%s) timed out after %s: %w", i+1, sanitizeCommandLine(command), databaseMigrationTimeout, err)
			}
			return fmt.Errorf("database migration %d failed: %w", i+1, err)
		}
	}
	log.Success("Database migrations applied")
	return nil
}

// ansiSequenceRe matches complete ESC-led terminal sequences: CSI, OSC and
// two-byte Fe controls. A bare ESC (no valid sequence) is dropped separately.
var ansiSequenceRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[@-_])`)

// isInvisibleFormat reports Unicode format characters that can reorder, hide
// or join displayed text. Left as-is, one bridge-declared line can be rendered
// as several harmless-looking commands while the shell still executes the
// original string. Kept separate so the display policy has one source of truth.
func isInvisibleFormat(r rune) bool {
	switch {
	case r >= 0x200b && r <= 0x200f, // zero-width space, ZWNJ, ZWJ, LRM, RLM
		r >= 0x202a && r <= 0x202e, // LRE, RLE, PDF, LRO, RLO
		r >= 0x2060 && r <= 0x206f, // word joiner, invisible operators, LRI/RLI/FSI/PDI
		r == 0x061c,                // Arabic letter mark
		r == 0xfeff:                // zero-width no-break space / BOM
		return true
	}
	return false
}

// sanitizeCommandLine removes terminal control characters from a bridge-declared
// command before it is displayed or logged. C0 (except tab and newline), C1,
// CSI/OSC sequences, bare ESC and invalid UTF-8 bytes are dropped. Tab and
// newline become visible "\t"/"\n" escapes; bidi and zero-width controls become
// visible "<U+XXXX>" tokens, so one line can never masquerade as several
// harmless commands. The command itself is never modified — only its
// presentation.
func sanitizeCommandLine(s string) string {
	s = ansiSequenceRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b { // ESC without a complete sequence
			i++
			continue
		}
		if c < utf8.RuneSelf {
			switch c {
			case '\t':
				b.WriteString(`\t`)
			case '\n':
				b.WriteString(`\n`)
			default:
				if (c < 0x20) || c == 0x7f {
					i++
					continue
				}
				b.WriteByte(c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 { // invalid byte
			i++
			continue
		}
		if r >= 0x80 && r <= 0x9f { // C1 controls
			i += size
			continue
		}
		if isInvisibleFormat(r) {
			fmt.Fprintf(&b, "<U+%04X>", r)
			i += size
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// migrationContext bundles the state shared by every cleanup handler.
type migrationContext struct {
	opts     *cliOptions
	fresh    map[string]bool
	log      *logger.Logger
	out      io.Writer // stdout: orphan lists are command data, progress goes to log
	failures []error   // hard failures under --prune, joined once at the end
}

// recordedError marks a failure that record has already turned into a hard
// error. A caller joining several recorded failures can then hand the aggregate
// back to fail without applying the policy (or the message prefix) twice.
type recordedError struct{ err error }

func (e recordedError) Error() string { return e.err.Error() }
func (e recordedError) Unwrap() error { return e.err }

// record handles a cleanup failure: --prune wraps it for the caller to join,
// other modes warn and continue. This keeps the single-failure contract in one
// place so every cleanup handler behaves the same way.
func (mc *migrationContext) record(err error, what string) error {
	if mc.opts.prune {
		return recordedError{fmt.Errorf("%s: %w", what, err)}
	}
	mc.log.Warn("could not %s: %v", what, err)
	return nil
}

// fail applies the cleanup failure policy at one failure site and remembers
// hard failures for the final errors.Join. It answers whether the caller may
// continue: under --prune the error is recorded (so the snapshot is not
// advanced) and the loop carries on collecting failures; an aggregate of
// already-recorded failures is likewise safe to continue from. Without --prune
// only a warning was emitted, so a caller that owns a hard error (a failed
// prompt, for example) must still return it.
func (mc *migrationContext) fail(err error, what string) bool {
	if _, ok := errors.AsType[recordedError](err); ok {
		return true
	}
	recorded := mc.record(err, what)
	if recorded == nil {
		return false
	}
	mc.failures = append(mc.failures, recorded)
	return true
}

// failCall applies the policy to an error returned by a cleanup handler. Its
// file-level failures are already recorded, so under --prune the aggregate only
// marks the run incomplete. Without --prune the error stays the caller's to
// return: logging it here would duplicate the report at the CLI boundary.
func (mc *migrationContext) failCall(err error, what string) bool {
	if _, ok := errors.AsType[recordedError](err); ok {
		return true
	}
	if !mc.opts.prune {
		return false
	}
	mc.failures = append(mc.failures, fmt.Errorf("%s: %w", what, err))
	return true
}

// cleanupDeferred reports whether a run must keep cleanup pending: non-interactive
// (or no TTY) without --prune. The previous snapshot stays on disk so a later
// --prune run can still find the orphaned files.
func cleanupDeferred(opts *cliOptions) bool {
	return !opts.prune && (opts.nonInteractive || !isTerminal())
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
//
// Under --prune a failed rewrite, delete or rename is a hard error: the caller
// must not advance the snapshot, or the failed cleanup becomes unrecoverable.
// Without --prune the advisory warn-and-continue behavior is preserved.
func applyMigrationActions(opts *cliOptions, diff *snapshot.Diff, fresh map[string]bool, log *logger.Logger, cmd *cobra.Command) (bool, error) {
	if diff == nil || diff.IsEmpty() {
		return true, nil
	}
	mc := &migrationContext{opts: opts, fresh: fresh, log: log, out: cmd.OutOrStdout()}

	// In --prune mode the tool owns the whole cleanup, so it also rewrites
	// identifier references in developer-owned files: the project's root
	// namespace after a project rename, and renamed entity fields. Generated
	// files are already regenerated with the new names; custom files are not,
	// so this is what keeps them compiling.
	if opts.prune {
		if diff.NamespaceRename != nil {
			for _, rel := range diff.NamespaceRename.Files {
				if mc.fresh[rel] {
					continue
				}
				rewrote, err := snapshot.RewriteFile(opts.outputDir, rel, snapshot.NamespaceTransform(diff.NamespaceRename))
				if err != nil {
					mc.fail(err, fmt.Sprintf("rewrite namespace in %s", rel))
					continue
				}
				if rewrote {
					mc.log.Info("updated namespace %s -> %s in %s", diff.NamespaceRename.OldNamespace, diff.NamespaceRename.NewNamespace, rel)
				}
			}
		}
		for _, fr := range diff.FieldRenames {
			for _, rel := range fr.CustomFiles {
				if mc.fresh[rel] {
					continue
				}
				rewrote, err := snapshot.RewriteFile(opts.outputDir, rel, snapshot.FieldTransform(fr))
				if err != nil {
					mc.fail(err, fmt.Sprintf("rewrite field references in %s", rel))
					continue
				}
				if rewrote {
					mc.log.Info("updated field %s -> %s in %s", fr.OldField, fr.NewField, rel)
				}
			}
		}
	}

	applied := true
	for _, del := range diff.Deleted {
		if len(del.Files) == 0 {
			continue
		}
		done, err := handleDeletedEntity(mc, del)
		if err != nil {
			if !mc.failCall(err, fmt.Sprintf("clean up removed entity %q", del.Name)) {
				return false, err
			}
			applied = false
			continue
		}
		applied = applied && done
	}

	for _, ren := range diff.Renamed {
		if len(ren.Files) == 0 {
			continue
		}
		done, err := handleRename(mc, ren, diff)
		if err != nil {
			if !mc.failCall(err, fmt.Sprintf("clean up renamed entity %q", ren.NewName)) {
				return false, err
			}
			applied = false
			continue
		}
		applied = applied && done
	}
	if err := errors.Join(mc.failures...); err != nil {
		return false, err
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

// cleanupPrompt carries the operation-specific wording of one cleanup kind.
// The frame is shared by deletes and renames; only the prompt, the deferred
// warning and the way a deferred file is listed differ.
type cleanupPrompt struct {
	selectFiles     func([]interactive.FileChoice) ([]string, error)
	deferredWarning string
	deferredLine    func(snapshot.FileRef) string
}

// runCleanup owns the frame shared by the delete and rename handlers: defer
// cleanup with a warning when there is no prompt to ask, otherwise select the
// files, skip freshly generated ones, run opFn per file and join the failures
// it recorded. Returns false when cleanup was deferred or a hard failure was
// recorded, so the caller keeps the previous snapshot.
func runCleanup(mc *migrationContext, files []snapshot.FileRef, p cleanupPrompt, opFn func(snapshot.FileRef)) (bool, error) {
	if cleanupDeferred(mc.opts) {
		mc.log.Warn("%s", p.deferredWarning)
		for _, f := range files {
			_, _ = fmt.Fprintf(mc.out, "    - %s\n", p.deferredLine(f))
		}
		return false, nil
	}
	selected, err := chooseMigrationFiles(mc.opts, files, p.selectFiles)
	if err != nil {
		return false, err
	}

	// Only failures recorded by this invocation count as its result; earlier
	// namespace/field rewrites are joined once at the end by the caller.
	start := len(mc.failures)
	for _, f := range files {
		if !selected[f.Path] || mc.fresh[f.Path] {
			continue
		}
		opFn(f)
	}
	if err := errors.Join(mc.failures[start:]...); err != nil {
		return false, err
	}
	return true, nil
}

// handleDeletedEntity deletes files of a removed entity. The delete itself is
// the only operation-specific step; selection, deferred cleanup and failure
// policy live in runCleanup.
func handleDeletedEntity(mc *migrationContext, del snapshot.DeletedEntity) (bool, error) {
	return runCleanup(mc, del.Files, cleanupPrompt{
		selectFiles: func(choices []interactive.FileChoice) ([]string, error) {
			return interactive.PromptDeleteFiles(del.Name, choices)
		},
		deferredWarning: fmt.Sprintf("Entity %q was removed from domain.yaml — orphaned files kept (re-run with --prune to delete):", del.Name),
		deferredLine: func(f snapshot.FileRef) string {
			return f.Path + customMark(f)
		},
	}, func(f snapshot.FileRef) {
		if err := snapshot.DeleteFile(mc.opts.outputDir, f.Path); err != nil {
			mc.fail(err, fmt.Sprintf("delete %s", f.Path))
			return
		}
		mc.log.Info("deleted %s", f.Path)
	})
}

// handleRename renames custom files after a prompt and removes stale generated
// files (they have been regenerated under the new entity name). In --prune mode
// the renamed files' contents are rewritten too (entity, namespace and field
// identifiers), so the developer-owned file keeps compiling after the rename.
// Returns false when cleanup was deferred (non-interactive without --prune).
func handleRename(mc *migrationContext, ren snapshot.Rename, d *snapshot.Diff) (bool, error) {
	return runCleanup(mc, ren.Files, cleanupPrompt{
		selectFiles: func(choices []interactive.FileChoice) ([]string, error) {
			return interactive.PromptRenameFiles(ren.OldName, ren.NewName, choices)
		},
		deferredWarning: fmt.Sprintf("Entity %q was renamed to %q — files kept (re-run with --prune to rename):", ren.OldName, ren.NewName),
		deferredLine: func(f snapshot.FileRef) string {
			return f.Path + " -> " + snapshot.RenameRelPath(f.Path, ren.OldName, ren.NewName)
		},
	}, func(f snapshot.FileRef) {
		if f.Custom {
			newRel := snapshot.RenameRelPath(f.Path, ren.OldName, ren.NewName)
			if mc.fresh[newRel] {
				// The destination was scaffolded this run by the custom
				// (overwrite: false) template. Replace it with the developer's
				// existing custom file so custom hooks survive the rename.
				// The helper commits the new file before removing the old one:
				// a failed write or removal cannot leave the developer without
				// either copy, and poisoned snapshot paths stay inside outputDir.
				transforms := snapshot.RenameTransforms(ren.OldName, ren.NewName, d)
				if err := snapshot.ReplaceScaffoldedFile(mc.opts.outputDir, f.Path, newRel, transforms...); err != nil {
					mc.fail(err, fmt.Sprintf("replace scaffolded %s", newRel))
					return
				}
				mc.log.Info("renamed %s -> %s (content updated)", f.Path, newRel)
				return
			}
			newRel, renamed, err := snapshot.RenameEntityFile(mc.opts.outputDir, f.Path, ren.OldName, ren.NewName)
			if err != nil {
				mc.fail(err, fmt.Sprintf("rename %s", f.Path))
				return
			}
			if renamed {
				if rewrote, rerr := snapshot.RewriteFile(mc.opts.outputDir, newRel, snapshot.RenameTransforms(ren.OldName, ren.NewName, d)...); rerr != nil {
					// No early return: the rename itself succeeded, so the
					// final Info line still reports it.
					mc.fail(rerr, fmt.Sprintf("rewrite content of %s", newRel))
				} else if rewrote {
					mc.log.Info("renamed %s -> %s (content updated)", f.Path, newRel)
					return
				}
				mc.log.Info("renamed %s -> %s", f.Path, newRel)
			}
			return
		}
		// Generated files have been regenerated under the new name — remove the stale copy.
		if err := snapshot.DeleteFile(mc.opts.outputDir, f.Path); err != nil {
			mc.fail(err, fmt.Sprintf("delete stale generated file %s", f.Path))
			return
		}
		mc.log.Info("removed stale generated file %s", f.Path)
	})
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
// validating each name against specmeta. Unknown addons are ignored with a
// warning so a typo is visible instead of looking like a successful opt-in.
func applyAddons(opts *cliOptions, project *ir.Project, log *logger.Logger) {
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
			continue
		}
		if log != nil {
			log.Warn("unknown addon %q ignored; valid addons: %s", addon, strings.Join(specmeta.Addons, ", "))
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

	log := commandLogger(cmd)
	hardErrors := make([]error, 0)
	for _, e := range validator.New(schema).Validate() {
		if e.Warning {
			log.Warn("%s", e.Error())
			continue
		}
		hardErrors = append(hardErrors, e)
	}
	if len(hardErrors) > 0 {
		return nil, fmt.Errorf("validation failed with %d error(s): %w", len(hardErrors), errors.Join(hardErrors...))
	}

	return schema, nil
}
