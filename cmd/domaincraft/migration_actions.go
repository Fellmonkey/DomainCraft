package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/interactive"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/snapshot"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
)

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

// fail applies the cleanup failure policy at one site. Under --prune the
// failure is recorded so the final errors.Join keeps the snapshot from
// advancing; other modes only warn and continue. Prompt errors never go through
// here — they are hard errors returned directly to the CLI boundary.
func (mc *migrationContext) fail(err error, what string) bool {
	if mc.opts.prune {
		mc.failures = append(mc.failures, fmt.Errorf("%s: %w", what, err))
		return true
	}
	mc.log.Warn("could not %s: %v", what, err)
	return false
}

// cleanupFailures marks an aggregate of file-level failures already recorded in
// migrationContext. The caller keeps collecting other entities but must not
// advance the snapshot; prompt errors are returned unwrapped and stay hard.
type cleanupFailures struct{ err error }

func (e cleanupFailures) Error() string { return e.err.Error() }
func (e cleanupFailures) Unwrap() error { return e.err }

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
			if _, ok := errors.AsType[cleanupFailures](err); !ok {
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
			if _, ok := errors.AsType[cleanupFailures](err); !ok {
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
	before := len(mc.failures)
	for _, f := range files {
		if !selected[f.Path] || mc.fresh[f.Path] {
			continue
		}
		opFn(f)
	}
	if err := errors.Join(mc.failures[before:]...); err != nil {
		return false, cleanupFailures{err: err}
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
