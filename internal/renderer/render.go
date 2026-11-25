// Package renderer renders the IR through bridge templates.
package renderer

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/packages"
)

// safeBridgeSource resolves a template or helper path declared by bridge.yaml
// and rejects paths that escape the bridge directory.
func safeBridgeSource(dir, rel string) (string, error) {
	path, _, err := fsutil.LocalPathWithin(dir, rel)
	if err != nil {
		return "", fmt.Errorf("unsafe bridge source path %q: %w", rel, err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve bridge dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return "", fmt.Errorf("resolve bridge dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil &&
		resolved != realDir && !strings.HasPrefix(resolved, realDir+string(filepath.Separator)) {
		return "", fmt.Errorf("bridge source path %q escapes the bridge directory", rel)
	}
	return path, nil
}

// safeOutputPathChecked is the shared implementation: lexical containment,
// absolute output dir, then the caller's symlink policy. Both wrappers keep the
// same error messages and return the same absolute target.
func safeOutputPathChecked(outputDir, renderedTarget string, refuse func(base, target string) error) (string, error) {
	absTarget, _, err := fsutil.LocalPathWithin(outputDir, renderedTarget)
	if err != nil {
		return "", fmt.Errorf("template target path %s: %w", renderedTarget, err)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	if err := refuse(absOutput, absTarget); err != nil {
		return "", fmt.Errorf("template target path %s: %w", renderedTarget, err)
	}
	return absTarget, nil
}

// safeOutputPath resolves a rendered template target against outputDir. It keeps
// the lexical containment check and additionally rejects a symlink at every
// existing path component (including the final one): os.Rename follows a
// symlink at the leaf, so a planted link would let a template write outside the
// output tree even when the lexical path is clean. Missing components are left
// to the caller's MkdirAll and are re-checked before the file is committed.
func safeOutputPath(outputDir, renderedTarget string) (string, error) {
	return safeOutputPathChecked(outputDir, renderedTarget, fsutil.RefuseSymlinks)
}

// safeOutputPathCached mirrors safeOutputPath but reuses directory components
// verified by earlier containment checks in the same Render. The final target
// component is always re-inspected. A nil cache gives a fully fresh walk.
func safeOutputPathCached(outputDir, renderedTarget string, verified map[string]bool) (string, error) {
	return safeOutputPathChecked(outputDir, renderedTarget, func(base, target string) error {
		return fsutil.RefuseSymlinksCached(base, target, verified)
	})
}

// beforeCommitHook is a test seam called once between staging and commit. It is
// nil in production; tests set it to mutate the filesystem at that exact point.
// Tests using it must not run in parallel.
var beforeCommitHook func()

// failWithTempCleanup closes and removes a staged temp file, joining any
// cleanup failure to the primary error so a leftover file cannot go unnoticed.
func failWithTempCleanup(err error, tmp *os.File, path string) error {
	cleanup := []error{err}
	if cerr := tmp.Close(); cerr != nil {
		cleanup = append(cleanup, cerr)
	}
	if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		cleanup = append(cleanup, rerr)
	}
	return errors.Join(cleanup...)
}

// stagedWrite is a fully rendered file held in a temp file next to its target.
// backup is the rename-aside path of the previous target, created only at
// commit time; existed=false means the target was created by this run.
// original is retained for callers that invoke rollbackWrites directly with
// in-memory bytes; Render itself no longer reads existing targets up front.
type stagedWrite struct {
	temp   string // absolute temp path, for staging cleanup and messages
	target string // absolute target path, for the returned list and messages
	backup string // absolute backup path, for messages

	// parentDir was verified free of symlinks at staging. dirRoot is opened
	// from it at commit; every commit/rename goes through that handle, so a
	// directory swapped for a symlink afterwards cannot redirect the write.
	parentDir  string
	parentInfo os.FileInfo
	dirRoot    *os.Root
	leaf       string // target name inside parentDir
	tempLeaf   string // temp name inside parentDir
	backupLeaf string // backup name inside parentDir

	original []byte
	existed  bool
	mode     os.FileMode
	record   RenderedFile
}

// rollbackWrites undoes committed writes in reverse order and removes every
// staged temp file. It is best-effort: every restore/remove failure is joined
// into the returned error so the caller can report it, while the original
// rendering error stays the primary cause.
func rollbackWrites(staged, committed []stagedWrite) error {
	var failures []error
	for _, w := range slices.Backward(committed) {
		if w.backup != "" {
			// The previous file was moved aside before the new one landed;
			// moving it back restores bytes, mode and timestamps exactly.
			var err error
			if w.dirRoot != nil {
				err = w.dirRoot.Rename(w.backupLeaf, w.leaf)
			} else {
				err = os.Rename(w.backup, w.target)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("restore %s: %w", w.target, err))
			}
			continue
		}
		if w.existed {
			var err error
			if w.dirRoot != nil {
				err = fsutil.AtomicWriteRoot(w.dirRoot, w.leaf, w.original)
			} else {
				err = fsutil.AtomicWrite(w.target, w.original)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("restore %s: %w", w.target, err))
				continue
			}
			if w.dirRoot != nil {
				err = chmodRoot(w.dirRoot, w.leaf, w.mode)
			} else {
				err = os.Chmod(w.target, w.mode)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("restore mode of %s: %w", w.target, err))
			}
			continue
		}
		var err error
		if w.dirRoot != nil {
			err = w.dirRoot.Remove(w.leaf)
		} else {
			err = os.Remove(w.target)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("remove %s: %w", w.target, err))
		}
	}
	for _, w := range staged {
		var err error
		if w.dirRoot != nil {
			err = w.dirRoot.Remove(w.tempLeaf)
		} else {
			err = os.Remove(w.temp)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("remove temp %s: %w", w.temp, err))
		}
	}
	return errors.Join(failures...)
}

// chmodRoot applies mode to a file inside root. os.Root has no Chmod method,
// so the file is reopened for the metadata-only change.
func chmodRoot(root *os.Root, rel string, mode os.FileMode) error {
	f, err := root.OpenFile(rel, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// rollbackError attaches rollback failures to the error that triggered them.
// The original error stays first, so it remains the primary cause for callers
// inspecting errors.Is / Error().
func rollbackError(err, rollbackErr error) error {
	if rollbackErr == nil {
		return err
	}
	return errors.Join(err, fmt.Errorf("rollback writes: %w", rollbackErr))
}

// parseHelpers loads the named templates ({{ define "name" }}) declared by
// every bridge in the chain. Bases are parsed first so a composed adapter
// inherits their helpers; the adapter's definitions win on conflicts.
func (r *Renderer) parseHelpers(funcMap template.FuncMap) (*template.Template, error) {
	var helpersTemplate *template.Template
	for _, src := range r.sources {
		if src.config.Helpers == "" {
			continue
		}
		helperPath, err := safeBridgeSource(src.dir, src.config.Helpers)
		if err != nil {
			return nil, err
		}
		helperBytes, err := os.ReadFile(helperPath) //nolint:gosec // helperPath passed safeBridgeSource validation
		if err != nil {
			return nil, fmt.Errorf("read helpers %s: %w", src.config.Helpers, err)
		}
		if helpersTemplate == nil {
			helpersTemplate, err = r.applyDelimiters(template.New("helpers").Funcs(funcMap)).Parse(string(helperBytes))
		} else {
			helpersTemplate, err = helpersTemplate.Parse(string(helperBytes))
		}
		if err != nil {
			return nil, fmt.Errorf("parse helpers %s: %w", src.config.Helpers, err)
		}
	}
	return helpersTemplate, nil
}

// renderSession carries the mutable state shared by the three Render phases.
// verified is local to one Render and never reused across the pre- and
// post-MkdirAll containment passes: MkdirAll can create components, so the
// latter pass always walks the path freshly.
type renderSession struct {
	outputDir string
	project   *ir.Project
	funcMap   template.FuncMap
	pkgs      map[string]string
	helpers   *template.Template

	verified map[string]bool
	// parentInfos is captured once per directory during staging; roots holds
	// one os.Root per directory, opened at commit and reused by all its files.
	parentInfos map[string]os.FileInfo
	roots       map[string]*os.Root
	staged      []stagedWrite
	committed   []stagedWrite
	manifest    []RenderedFile
}

// dirRoot returns the handle for a staged file's parent directory. The handle
// is opened at commit and checked against the FileInfo captured at staging:
// if the directory was replaced in between, the mismatch aborts the render
// instead of following the replacement.
//
// Scope is deliberate: only the two renames per file go through Root. Opening
// every staging operation (MkdirAll, Lstat, CreateTemp) through Root re-resolves
// each component and makes a warm render ~2.9x slower — see the A/B numbers in
// BenchmarkSafeOutputPathWarm. The symlink containment checks already cover
// staging, so Root is needed only for the final check-then-use window.
func (s *renderSession) dirRoot(w *stagedWrite) (*os.Root, error) {
	if root := s.roots[w.parentDir]; root != nil {
		return root, nil
	}
	root, err := os.OpenRoot(w.parentDir)
	if err != nil {
		return nil, fmt.Errorf("open output directory %s: %w", w.parentDir, err)
	}
	info, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("inspect output directory %s: %w", w.parentDir, err)
	}
	if !os.SameFile(w.parentInfo, info) {
		_ = root.Close()
		return nil, fmt.Errorf("output directory %s was replaced before commit", w.parentDir)
	}
	if s.roots == nil {
		s.roots = make(map[string]*os.Root)
	}
	s.roots[w.parentDir] = root
	return root, nil
}

// fail drops every staged temp file and restores committed files, attaching
// any rollback failure to the primary error. The phase that detects a failure
// calls it once, while the session still holds the exact writes performed.
func (s *renderSession) fail(err error) error {
	return rollbackError(err, rollbackWrites(s.staged, s.committed))
}

// stageTemplates parses each selected template and renders every target into a
// temp file next to it. Nothing is written to outputDir until all templates
// executed successfully; a failure drops the temp files staged so far.
func (r *Renderer) stageTemplates(s *renderSession) error {
	for _, src := range r.sources {
		for _, spec := range src.config.Templates {
			// Check if template should be rendered based on "When" condition
			if !r.shouldRender(spec, s.project, s.pkgs) {
				continue
			}

			sourcePath, err := safeBridgeSource(src.dir, spec.Source)
			if err != nil {
				return s.fail(err)
			}
			tplBytes, err := os.ReadFile(sourcePath) //nolint:gosec // sourcePath passed safeBridgeSource validation
			if err != nil {
				return s.fail(fmt.Errorf("read template %s: %w", spec.Source, err))
			}

			tplName := filepath.Base(spec.Source)
			var parsedTemplate *template.Template
			if s.helpers != nil {
				// Clone helpers so all named templates are available
				parsedTemplate, err = s.helpers.Clone()
				if err != nil {
					return s.fail(fmt.Errorf("clone helpers for %s: %w", spec.Source, err))
				}
				parsedTemplate, err = r.applyDelimiters(parsedTemplate.New(tplName)).Parse(string(tplBytes))
			} else {
				parsedTemplate, err = r.applyDelimiters(template.New(tplName).Funcs(s.funcMap)).Parse(string(tplBytes))
			}
			if err != nil {
				return s.fail(fmt.Errorf("parse template %s: %w", spec.Source, err))
			}

			contexts, err := r.renderContexts(spec.For, s.project, s.pkgs)
			if err != nil {
				return s.fail(err)
			}

			// A target pattern is static for the spec; only the context changes.
			// Parse once instead of once per (entity, pattern) pair.
			targetPatterns := spec.TargetPatterns()
			targetTemplates := make([]*template.Template, len(targetPatterns))
			for i, pattern := range targetPatterns {
				targetTemplates[i], err = template.New("path").Funcs(s.funcMap).Parse(pattern)
				if err != nil {
					return s.fail(fmt.Errorf("parse target path %q: %w", pattern, err))
				}
			}

			for _, context := range contexts {
				// Check if this specific context should be rendered based on "When" condition
				if !r.shouldRenderContext(spec, context) {
					continue
				}

				entityName := ""
				if context.Entity != nil {
					entityName = context.Entity.Name
				}

				for _, targetTpl := range targetTemplates {
					renderedTarget, err := executeTemplateString(targetTpl, context)
					if err != nil {
						return s.fail(fmt.Errorf("render target path: %w", err))
					}

					absTarget, err := safeOutputPathCached(s.outputDir, renderedTarget, s.verified)
					if err != nil {
						return s.fail(err)
					}

					relPath := filepath.ToSlash(renderedTarget)
					record := RenderedFile{
						Path:    relPath,
						Entity:  entityName,
						Custom:  spec.IsCustom(),
						Written: true,
					}

					// Scaffold semantics: overwrite: false files are created only once.
					// The developer owns them afterwards, so they survive regeneration.
					if spec.IsCustom() {
						if _, statErr := os.Stat(absTarget); statErr == nil {
							record.Written = false
							s.manifest = append(s.manifest, record)
							continue
						}
					}

					if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil { //nolint:gosec // generated project dirs must stay world-readable
						return s.fail(fmt.Errorf("create output dir: %w", err))
					}
					// MkdirAll may have created the missing components above; re-check
					// with a fully fresh walk (no cached directories) so a symlink
					// planted in the meantime is still rejected.
					if _, err := safeOutputPath(s.outputDir, renderedTarget); err != nil {
						return s.fail(err)
					}
					// Capture the verified parent directory now. Commit opens an
					// os.Root from this path and compares the directory identity,
					// so a swap before commit is detected instead of followed.
					parentDir := filepath.Dir(absTarget)
					parentInfo, ok := s.parentInfos[parentDir]
					if !ok {
						parentInfo, err = os.Lstat(parentDir)
						if err != nil {
							return s.fail(fmt.Errorf("inspect output directory for %s: %w", renderedTarget, err))
						}
						s.parentInfos[parentDir] = parentInfo
					}

					existed := false
					mode := os.FileMode(0o644) // os.Create's default under the usual umask
					if info, statErr := os.Lstat(absTarget); statErr == nil {
						if info.Mode()&os.ModeSymlink != 0 {
							return s.fail(fmt.Errorf("template target path escapes output directory through a symlink: %s", renderedTarget))
						}
						existed = true
						mode = info.Mode().Perm()
						// The previous bytes are not read here: the commit phase moves
						// the whole file aside and moves it back on rollback.
					} else if !errors.Is(statErr, os.ErrNotExist) {
						return s.fail(fmt.Errorf("inspect output file %s: %w", renderedTarget, statErr))
					}

					// Render into a temp file in the target's own directory so the
					// final rename stays on the same filesystem. The prior file is
					// never truncated, even if Execute or Close fails.
					tmp, err := os.CreateTemp(filepath.Dir(absTarget), ".domaincraft-render-*")
					if err != nil {
						return s.fail(fmt.Errorf("create temp output file: %w", err))
					}
					tmpPath := tmp.Name()
					// CreateTemp uses 0600; generated files stay readable as before.
					if err := tmp.Chmod(mode); err != nil {
						return s.fail(failWithTempCleanup(fmt.Errorf("set mode on temp output %s: %w", tmpPath, err), tmp, tmpPath))
					}
					if err := parsedTemplate.Execute(tmp, context); err != nil {
						return s.fail(failWithTempCleanup(fmt.Errorf("execute template: %w", err), tmp, tmpPath))
					}
					if err := tmp.Close(); err != nil {
						if rerr := os.Remove(tmpPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
							return s.fail(errors.Join(fmt.Errorf("close temp output %s: %w", tmpPath, err), rerr))
						}
						return s.fail(fmt.Errorf("close temp output %s: %w", tmpPath, err))
					}
					s.staged = append(s.staged, stagedWrite{
						temp:       tmpPath,
						target:     absTarget,
						parentDir:  parentDir,
						parentInfo: parentInfo,
						leaf:       filepath.Base(absTarget),
						tempLeaf:   filepath.Base(tmpPath),
						existed:    existed,
						mode:       mode,
						record:     record,
					})
					s.manifest = append(s.manifest, record)
				}
			}
		}
	}
	return nil
}

// commitWrites commits staged files in order. An existing target is renamed
// aside into a sibling backup before the rendered temp file takes its place;
// rollback moves the backup back, so no prior content is read into memory.
func (r *Renderer) commitWrites(s *renderSession) error {
	if beforeCommitHook != nil {
		beforeCommitHook()
	}
	for _, w := range s.staged {
		root, err := s.dirRoot(&w)
		if err != nil {
			return s.fail(err)
		}
		w.dirRoot = root
		// os.Lstat sees only the leaf, which is enough here: the renames below
		// go through root, so a parent swapped for a symlink cannot redirect
		// them even if this stat saw a regular file.
		if info, err := os.Lstat(w.target); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return s.fail(fmt.Errorf("template target path escapes output directory through a symlink: %s", w.record.Path))
			}
			// The staged temp name is already unique in this directory.
			w.backup = w.temp + ".bak"
			w.backupLeaf = w.tempLeaf + ".bak"
			if err := root.Rename(w.leaf, w.backupLeaf); err != nil {
				return s.fail(fmt.Errorf("back up output file %s: %w", w.record.Path, err))
			}
			w.existed = true
			w.mode = info.Mode().Perm()
		} else if errors.Is(err, os.ErrNotExist) {
			// The target vanished after staging. Restore the pre-commit state:
			// this is now a new file, so rollback must remove it, not write nil
			// bytes through the staging-time `existed` flag.
			w.existed = false
			w.backup = ""
		} else {
			return s.fail(fmt.Errorf("inspect output file %s: %w", w.record.Path, err))
		}
		// Record the write before the final rename so a failure here still
		// restores the backup and removes the staged temp file.
		s.committed = append(s.committed, w)
		if err := root.Rename(w.tempLeaf, w.leaf); err != nil {
			return s.fail(fmt.Errorf("commit output file %s: %w", w.record.Path, err))
		}
	}
	return nil
}

// cleanupBackups removes the rename-aside backups after a successful commit.
// A leftover backup is only warned about: the generated files are correct and
// rollback no longer consumes those backups.
func (r *Renderer) cleanupBackups(s *renderSession) {
	for _, w := range s.committed {
		if w.backup != "" {
			var err error
			if w.dirRoot != nil {
				err = w.dirRoot.Remove(w.backupLeaf)
			} else {
				err = os.Remove(w.backup)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				r.log.Warn("remove backup %s: %v", w.backup, err)
			}
		}
	}
}

// Render renders the IR through every bridge in the chain and returns the
// written paths and the file manifest. Rendering is staged in temp files and
// committed only after every template succeeded; a failure at any point removes
// the staged files and restores any output files that were already replaced.
func (r *Renderer) Render(project *ir.Project, outputDir string) ([]string, []RenderedFile, error) {
	if project == nil {
		return nil, nil, errors.New("ir project is nil")
	}
	if outputDir == "" {
		outputDir = r.config().OutputDir
	}

	funcMap, err := r.buildFuncMap()
	if err != nil {
		return nil, nil, fmt.Errorf("build template functions: %w", err)
	}

	// Resolve package versions once — avoids repeated HTTP requests per template.
	// The project's platform constrains the major version; the core bakes in no
	// language-specific framework default.
	pkgs := r.resolvePackages(project)

	helpersTemplate, err := r.parseHelpers(funcMap)
	if err != nil {
		return nil, nil, err
	}

	session := &renderSession{
		outputDir:   outputDir,
		project:     project,
		funcMap:     funcMap,
		pkgs:        pkgs,
		helpers:     helpersTemplate,
		verified:    make(map[string]bool),
		parentInfos: make(map[string]os.FileInfo),
		roots:       make(map[string]*os.Root),
		staged:      make([]stagedWrite, 0),
		// committed is empty during staging; commitWrites appends as files land.
		committed: make([]stagedWrite, 0),
		manifest:  make([]RenderedFile, 0),
	}

	defer func() {
		for _, root := range session.roots {
			_ = root.Close()
		}
	}()

	if err := r.stageTemplates(session); err != nil {
		return nil, nil, err
	}
	if err := r.commitWrites(session); err != nil {
		return nil, nil, err
	}
	r.cleanupBackups(session)

	written := make([]string, 0, len(session.committed))
	for _, w := range session.committed {
		written = append(written, w.target)
	}
	return written, session.manifest, nil
}

func (r *Renderer) shouldRender(spec TemplateSpec, project *ir.Project, pkgs map[string]string) bool {
	// Entity-level When conditions are checked per-context in shouldRenderContext.
	// Project-level When conditions are checked here.
	if spec.When == "" || spec.For == "entity" {
		return true
	}
	cfg := r.config()
	return r.shouldRenderContext(spec, RenderContext{Project: project, Bridge: &cfg, Packages: pkgs, Migration: r.migration, SeedData: r.seedData, MockData: r.mockData})
}

func (r *Renderer) shouldRenderContext(spec TemplateSpec, context RenderContext) bool {
	if spec.When == "" {
		return true
	}
	// Addon conditions accept a suffix after a colon, e.g. "hasAddon:dapr" /
	// "notHasAddon:dapr". Bare "hasAddon" treats the presence of any addon as
	// true.
	if cond, name, ok := strings.Cut(spec.When, ":"); ok {
		switch strings.TrimSpace(cond) {
		case whenAddon:
			if context.Project != nil {
				if name == "" {
					return len(context.Project.Addons) > 0
				}
				return context.Project.HasAddon(strings.TrimSpace(name))
			}
			return false
		case whenNotAddon:
			if context.Project != nil {
				if name == "" {
					return len(context.Project.Addons) == 0
				}
				return !context.Project.HasAddon(strings.TrimSpace(name))
			}
			return true
		}
	}
	switch spec.When {
	case whenSeed:
		// Only render seed templates if there's actual seed data.
		return context.HasSeedData()
	case whenEnums:
		// Only render enum templates if there are enums defined
		if context.Project != nil && len(context.Project.Enums) > 0 {
			return true
		}
		return false
	case whenOwnerTokens:
		// Only render owner resolver if any entity uses @Owner tokens
		if context.Project != nil {
			for _, e := range context.Project.Entities {
				if e.Permissions != nil && e.Permissions.HasOwnerToken() {
					return true
				}
			}
		}
		return false
	case whenAuth:
		return context.Project != nil && context.Project.HasAuth()
	case whenMigration:
		// True when the core computed a non-empty schema migration plan.
		return context.HasMigration()
	case whenMockData:
		// True when the context carries generated mock data.
		return context.HasMockData()
	default:
		// Defensive only: loaded manifests are validated against the closed
		// vocabulary in validateWhenConditions, so this is unreachable for
		// bridge.yaml-driven templates.
		return true
	}
}

// resolvePackages resolves all package versions from the package registry,
// cached per bridge so repeated runs don't hit the registry every time.
// When the renderer is composed (extends chain), registry_packages from all
// bridges are merged base-first so inner layers can declare their own
// dependencies; the adapter's values win on conflicts.
// targetFramework returns the project's declared platform, then the framework
// declared by the bridge chain (adapter first, base later). An absent framework
// leaves version selection unconstrained — the core bakes in no TFM.
func (r *Renderer) targetFramework(project *ir.Project) string {
	if project != nil && project.Platform != "" {
		return project.Platform
	}
	for _, src := range slices.Backward(r.sources) {
		if fw := src.config.TargetFramework; fw != "" {
			return fw
		}
	}
	return ""
}

func (r *Renderer) resolvePackages(project *ir.Project) map[string]string {
	framework := r.targetFramework(project)

	// Merge registry_packages from all sources in the chain (base-first,
	// adapter wins on conflicts — consistent with type_mappings merge).
	merged := make(map[string]string)
	for _, src := range r.sources {
		maps.Copy(merged, src.config.RegistryPackages)
	}
	if len(merged) == 0 {
		return nil
	}

	// Use the adapter's registry_url; fall back to any source with one.
	registryURL := r.config().RegistryURL
	if registryURL == "" {
		for _, src := range slices.Backward(r.sources) {
			if src.config.RegistryURL != "" {
				registryURL = src.config.RegistryURL
				break
			}
		}
	}
	if registryURL == "" {
		r.log.Warn("no registry_url configured in any bridge in the chain; skipping package resolution")
		return nil
	}

	result := make(map[string]string, len(merged))
	for _, key := range slices.Sorted(maps.Keys(merged)) {
		packageID := merged[key]
		version, err := packages.ResolveVersionCachedForFramework(r.cacheNamespace(), registryURL, packageID, framework)
		if err != nil {
			// A cache-write failure still carries the resolved version; warn
			// once and use it instead of emitting a package-less project.
			r.log.Warn("failed to resolve package %s: %v", packageID, err)
		}
		if version != "" {
			result[key] = version
		}
	}
	return result
}

// cacheNamespace returns a stable, filesystem-safe identifier for this bridge,
// used to keep its package-version cache separate from other bridges'. The
// bridge.yaml name is preferred; a path-based fallback covers nameless bridges.
func (r *Renderer) cacheNamespace() string {
	name := strings.ToLower(strings.TrimSpace(r.config().Name))
	if name == "" {
		name = filepath.Base(r.adapterDir())
	}

	var b strings.Builder
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func (r *Renderer) renderContexts(scope string, project *ir.Project, pkgs map[string]string) ([]RenderContext, error) {
	cfg := r.config()
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "entity":
		contexts := make([]RenderContext, 0, len(project.Entities))
		for i := range project.Entities {
			contexts = append(contexts, RenderContext{Project: project, Entity: &project.Entities[i], Bridge: &cfg, Packages: pkgs, Migration: r.migration, SeedData: r.seedData, MockData: r.mockData})
		}
		return contexts, nil
	case "project":
		return []RenderContext{{Project: project, Bridge: &cfg, Packages: pkgs, Migration: r.migration, SeedData: r.seedData, MockData: r.mockData}}, nil
	default:
		return nil, fmt.Errorf("unsupported template scope '%s'", scope)
	}
}
