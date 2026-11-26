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
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/interactive"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/seed"
	"github.com/DomainCraft/DomainCraft/internal/snapshot"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
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
	return logger.NewWithWriter(cmd.ErrOrStderr())
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
		// `generate` itself takes none. Validate the pairing here, before
		// RunE writes any files.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return err
			}
			if !cmd.Flags().Changed("admin") || opts.adminBridge != "admin-alpine" {
				return asUsageError(fmt.Errorf("unexpected positional argument %q: use --admin <bridge-id>", args[0]))
			}
			opts.adminBridge = args[0]
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGenerate(opts, cmd)
		},
	}

	// --admin [bridge-id] — optional value, defaults to "admin-alpine" when flag
	// is present without a value. NoOptDefVal is used for the default, and the
	// Args validator reclaims a space-separated value so both `--admin <id>`
	// and `--admin=<id>` work.
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
// and applies addons. Bridge resolution stays between validation and IR
// building so prompts, log lines and errors keep their original order.
func loadAndValidateDomain(opts *cliOptions, cmd *cobra.Command, log *logger.Logger) (*ir.Project, string, error) {
	log.Info("Parsing %s", opts.domainFile)
	schema, err := loadAndValidate(opts, cmd)
	if err != nil {
		return nil, "", err
	}
	log.Success("Schema valid (%d entities)", len(schema.Entities))

	bridgePath, bridgeName, err := resolveBridgeInteractive(opts, log)
	if err != nil {
		return nil, "", err
	}
	opts.bridgePath = bridgePath

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
func runGenerate(opts *cliOptions, cmd *cobra.Command) error {
	log := commandLogger(cmd)
	ctx := &commandContext{opts: opts, cmd: cmd, log: log}

	// Flag syntax is checked before reading the domain, prompting the user,
	// building the IR or cloning a bridge.
	replacements, err := parseReplaceFlags(opts.replaceFlags)
	if err != nil {
		return asUsageError(err)
	}

	irProject, bridgeName, err := loadAndValidateDomain(opts, cmd, log)
	if err != nil {
		return err
	}

	// --- Schema snapshot / migration diff ---
	snapPath := snapshot.Path(opts.outputDir)
	oldSnapshot, err := snapshot.Load(snapPath)
	if err != nil {
		return fmt.Errorf("load schema snapshot: %w", err)
	}
	migrationDiff := snapshot.ComputeDiff(oldSnapshot, irProject, opts.outputDir, log.Warn)

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
	ctx.chainHasCommunity = chainHasCommunity
	// Build the renderer before writing anything so the consent decision can
	// be made up front: a rejected --migrate must not leave generated files
	// or pruned files behind.
	rendererInstance, err := newBridgeRenderer(renderOptions{
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
	if err := planBridgeMigrations(ctx, migrationDiff, rendererInstance.MigrationConfig()); err != nil {
		return err
	}

	log.Info("Rendering via %s", opts.bridgePath)
	writtenFiles, manifest, err := rendererInstance.Render(irProject, opts.outputDir)
	if err != nil {
		return err
	}

	log.Success("Generated %d file(s) into %s", len(writtenFiles), opts.outputDir)

	// --- Admin panel generation ---
	adminManifest, err := generateAdminPanel(ctx, irProject, seedData, mockDataset)
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
	ctx.cleanupApplied = applied
	if err := runBridgeDatabaseMigrations(ctx, rendererInstance.MigrationConfig()); err != nil {
		return err
	}

	// --- Smart refactoring reports ---
	// Warnings and the multi-line reports are both human-facing diagnostics:
	// they go to stderr so stdout stays usable for piping. Order is fixed.
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
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), report)
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

// commandContext groups the state threaded through the generation pipeline's
// helper steps: parsed flags, the command (for stdio), its logger, and the two
// chain/cleanup facts the migration helpers act on.
