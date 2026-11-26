// Package renderer renders the IR through bridge templates.
package renderer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/ir"
)

// readBridgeSource reads a template, helper or mapping declared by bridge.yaml.
// The path is confined to the bridge directory and read through one os.Root
// transaction, so a component swapped for a symlink between the check and the
// read cannot pull contents from outside the bridge.
func readBridgeSource(dir, rel string) ([]byte, error) {
	path, relative, err := fsutil.LocalPathWithin(dir, rel)
	if err != nil {
		return nil, fmt.Errorf("unsafe bridge source path %q: %w", rel, err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve bridge dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return nil, fmt.Errorf("resolve bridge dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil &&
		resolved != realDir && !strings.HasPrefix(resolved, realDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("bridge source path %q escapes the bridge directory", rel)
	}
	root, err := os.OpenRoot(realDir)
	if err != nil {
		return nil, fmt.Errorf("open bridge root: %w", err)
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(relative)
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

// failWithTempCleanup closes and removes a staged temp file, joining any
// cleanup failure to the primary error so a leftover file cannot go unnoticed.
func failWithTempCleanup(err error, tmp *os.File, path string) error {
	cleanup := []error{err}
	// The helper is also used after a failed Close: a second Close returns
	// ErrClosed, which is not a new cleanup failure.
	if cerr := tmp.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
		cleanup = append(cleanup, cerr)
	}
	if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		cleanup = append(cleanup, rerr)
	}
	return errors.Join(cleanup...)
}

// stagedWrite is a fully rendered file held in a temp file next to its target.
// backup is the rename-aside path of the previous target, created only at
// commit time; an empty backup means the target was created by this run.
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

	custom bool // overwrite:false — developer-owned, must not be replaced at commit
	record RenderedFile
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
		// commitWrites always creates a backup for an existing target, so
		// there is no in-memory restore path here: a new file is removed.
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
	// Staged writes have no dirRoot: handles are opened only for committed
	// files at commit time, so temp cleanup goes through the plain path.
	for _, w := range staged {
		if err := os.Remove(w.temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("remove temp %s: %w", w.temp, err))
		}
	}
	return errors.Join(failures...)
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
		helperBytes, err := readBridgeFile(src, src.config.Helpers)
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
	// customTargets tracks scaffold-once paths planned in this run. Without
	// it, two contexts with the same constant target both see "missing" on
	// disk and the second write overwrites the first.
	customTargets map[string]bool
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

// compiledSpec is the static part of a template spec: the body and its target
// path templates. It depends only on bridge files, not on the domain model, so
// both the disk and in-memory renderers compile it the same way.
type compiledSpec struct {
	body    *template.Template
	targets []*template.Template
}

func (r *Renderer) compileSpec(src sourceConfig, spec TemplateSpec, funcMap template.FuncMap, helpers *template.Template) (*compiledSpec, error) {
	tplBytes, err := readBridgeFile(src, spec.Source)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", spec.Source, err)
	}

	tplName := filepath.Base(spec.Source)
	var body *template.Template
	if helpers != nil {
		body, err = helpers.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone helpers for %s: %w", spec.Source, err)
		}
		body, err = r.applyDelimiters(body.New(tplName)).Parse(string(tplBytes))
	} else {
		body, err = r.applyDelimiters(template.New(tplName).Funcs(funcMap)).Parse(string(tplBytes))
	}
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", spec.Source, err)
	}

	patterns := spec.TargetPatterns()
	targets := make([]*template.Template, len(patterns))
	for i, pattern := range patterns {
		targets[i], err = template.New("path").Funcs(funcMap).Parse(pattern)
		if err != nil {
			return nil, fmt.Errorf("parse target path %q: %w", pattern, err)
		}
	}
	return &compiledSpec{body: body, targets: targets}, nil
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

			compiled, err := r.compileSpec(src, spec, s.funcMap, s.helpers)
			if err != nil {
				return s.fail(err)
			}
			parsedTemplate := compiled.body
			targetTemplates := compiled.targets

			contexts, err := r.renderContexts(spec.For, s.project, s.pkgs)
			if err != nil {
				return s.fail(err)
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
					if err := stageOneTarget(s, spec, context, parsedTemplate, targetTpl, entityName); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// resolvedTarget is one rendered target path plus the manifest record shared
// by the scaffold gate and the staging step.
type resolvedTarget struct {
	rendered string // raw rendered target, used verbatim in error messages
	abs      string // absolute, containment-checked destination
	record   RenderedFile
}

// resolveTargetPath executes the target pattern, re-checks containment with
// the per-render cache and builds the manifest record.
func (s *renderSession) resolveTargetPath(target *template.Template, context RenderContext, spec TemplateSpec, entityName string) (resolvedTarget, error) {
	renderedTarget, err := executeTemplateString(target, context)
	if err != nil {
		return resolvedTarget{}, fmt.Errorf("render target path: %w", err)
	}
	absTarget, err := safeOutputPathCached(s.outputDir, renderedTarget, s.verified)
	if err != nil {
		return resolvedTarget{}, err
	}
	return resolvedTarget{
		rendered: renderedTarget,
		abs:      absTarget,
		record: RenderedFile{
			Path:    filepath.ToSlash(renderedTarget),
			Entity:  entityName,
			Custom:  spec.IsCustom(),
			Written: true,
		},
	}, nil
}

// scaffoldAllowed applies overwrite:false semantics: a target planned earlier
// in this run or already present on disk stays developer-owned. A skipped
// target is recorded in the manifest with Written=false and is not staged.
func (s *renderSession) scaffoldAllowed(path resolvedTarget) bool {
	if !path.record.Custom {
		return true
	}
	if s.customTargets[path.abs] {
		path.record.Written = false
		s.manifest = append(s.manifest, path.record)
		return false
	}
	if _, statErr := os.Stat(path.abs); statErr == nil {
		path.record.Written = false
		s.manifest = append(s.manifest, path.record)
		return false
	}
	s.customTargets[path.abs] = true
	return true
}

// stageTempFile creates the parent directory, re-checks containment after
// MkdirAll, captures the parent's identity and the previous file mode, then
// renders the body into a temp file next to the target. On success the staged
// write and its manifest record are appended; on failure the temp file is
// cleaned up.
func (s *renderSession) stageTempFile(path resolvedTarget, body *template.Template, context RenderContext) error {
	if err := os.MkdirAll(filepath.Dir(path.abs), 0o755); err != nil { //nolint:gosec // generated project dirs must stay world-readable
		return fmt.Errorf("create output dir: %w", err)
	}
	// MkdirAll may have created the missing components above; re-check
	// with a fully fresh walk (no cached directories) so a symlink
	// planted in the meantime is still rejected.
	if _, err := safeOutputPath(s.outputDir, path.rendered); err != nil {
		return err
	}
	// Capture the verified parent directory now. Commit opens an
	// os.Root from this path and compares the directory identity,
	// so a swap before commit is detected instead of followed.
	parentDir := filepath.Dir(path.abs)
	parentInfo, ok := s.parentInfos[parentDir]
	if !ok {
		info, err := os.Lstat(parentDir)
		if err != nil {
			return fmt.Errorf("inspect output directory for %s: %w", path.rendered, err)
		}
		parentInfo = info
		s.parentInfos[parentDir] = parentInfo
	}

	mode := os.FileMode(0o644) // os.Create's default under the usual umask
	if info, statErr := os.Lstat(path.abs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("template target path escapes output directory through a symlink: %s", path.rendered)
		}
		mode = info.Mode().Perm()
		// The previous bytes are not read here: the commit phase moves
		// the whole file aside and moves it back on rollback.
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect output file %s: %w", path.rendered, statErr)
	}

	// Render into a temp file in the target's own directory so the
	// final rename stays on the same filesystem. The prior file is
	// never truncated, even if Execute or Close fails.
	tmp, err := os.CreateTemp(filepath.Dir(path.abs), ".domaincraft-render-*")
	if err != nil {
		return fmt.Errorf("create temp output file: %w", err)
	}
	tmpPath := tmp.Name()
	// CreateTemp uses 0600; generated files stay readable as before.
	if err := tmp.Chmod(mode); err != nil {
		return failWithTempCleanup(fmt.Errorf("set mode on temp output %s: %w", tmpPath, err), tmp, tmpPath)
	}
	if err := body.Execute(tmp, context); err != nil {
		return failWithTempCleanup(fmt.Errorf("execute template: %w", err), tmp, tmpPath)
	}
	if err := tmp.Close(); err != nil {
		return failWithTempCleanup(fmt.Errorf("close temp output %s: %w", tmpPath, err), tmp, tmpPath)
	}
	s.staged = append(s.staged, stagedWrite{
		temp:       tmpPath,
		target:     path.abs,
		parentDir:  parentDir,
		parentInfo: parentInfo,
		leaf:       filepath.Base(path.abs),
		tempLeaf:   filepath.Base(tmpPath),
		custom:     path.record.Custom,
		record:     path.record,
	})
	s.manifest = append(s.manifest, path.record)
	return nil
}

// stageOneTarget resolves one rendered target path, applies the scaffold and
// symlink policies, and stages the rendered bytes. The order of the three
// steps is load-bearing: it is preserved verbatim from the original loop.
func stageOneTarget(s *renderSession, spec TemplateSpec, context RenderContext, body, target *template.Template, entityName string) error {
	path, err := s.resolveTargetPath(target, context, spec, entityName)
	if err != nil {
		return s.fail(err)
	}
	if !s.scaffoldAllowed(path) {
		return nil
	}
	if err := s.stageTempFile(path, body, context); err != nil {
		return s.fail(err)
	}
	return nil
}

// commitWrites commits staged files in order. An existing target is renamed
// aside into a sibling backup before the rendered temp file takes its place;
// rollback moves the backup back, so no prior content is read into memory.
func (r *Renderer) commitWrites(s *renderSession) error {
	if hook := r.beforeCommitHook; hook != nil {
		hook()
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
			// A scaffold-once target was missing at staging but appeared before
			// commit: it is now developer-owned. Backing it up and deleting the
			// backup after commit would silently replace it, so preserve it and
			// abort. Rollback still restores writes committed earlier in this run.
			if w.custom {
				return s.fail(fmt.Errorf("custom file %s appeared before commit and was preserved", w.record.Path))
			}
			// The staged temp name is already unique in this directory.
			w.backup = w.temp + ".bak"
			w.backupLeaf = w.tempLeaf + ".bak"
			if err := root.Rename(w.leaf, w.backupLeaf); err != nil {
				return s.fail(fmt.Errorf("back up output file %s: %w", w.record.Path, err))
			}
		} else if errors.Is(err, os.ErrNotExist) {
			// The target vanished after staging: rollback must remove the new
			// file rather than restore a backup that was never created.
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
		committed:     make([]stagedWrite, 0),
		manifest:      make([]RenderedFile, 0),
		customTargets: make(map[string]bool),
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
	cond, name, _ := strings.Cut(spec.When, ":")
	entry, ok := whenSpecs[strings.TrimSpace(cond)]
	if !ok {
		// Defensive only: loaded manifests are validated against the closed
		// vocabulary in validateWhenConditions, so this is unreachable for
		// bridge.yaml-driven templates.
		return true
	}
	return entry.eval(context, name)
}

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
