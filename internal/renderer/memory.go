package renderer

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

// NewFromFS loads a bridge chain from an fs.FS, typically the `//go:embed
// bridges` tree shipped inside the CLI and the WASM binary. bridgePaths are
// base-first; the adapter is last. The disk renderer's contract, condition and
// type-mapping rules are shared with the CLI through parseSourceConfig.
func NewFromFS(fsys fs.FS, bridgePaths []string, log *logger.Logger) (*Renderer, error) {
	if fsys == nil {
		return nil, errors.New("bridge filesystem is nil")
	}
	sources := make([]sourceConfig, 0, len(bridgePaths))
	for _, raw := range bridgePaths {
		dir := path.Clean(filepath.ToSlash(raw))
		if path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") {
			return nil, fmt.Errorf("unsafe bridge path %q", raw)
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, "bridge.yaml"))
		if err != nil {
			return nil, fmt.Errorf("read bridge config %s: %w", dir, err)
		}
		src, err := parseSourceConfig(data, dir)
		if err != nil {
			return nil, fmt.Errorf("bridge %s: %w", dir, err)
		}
		warnUndeclaredContract(src, log)
		src.readSource = func(dir, rel string) ([]byte, error) {
			clean := path.Clean(filepath.ToSlash(rel))
			if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
				return nil, fmt.Errorf("unsafe bridge source path %q", rel)
			}
			return fs.ReadFile(fsys, path.Join(dir, clean))
		}
		sources = append(sources, src)
	}
	if len(sources) == 0 {
		return nil, errors.New("empty bridge chain")
	}
	return &Renderer{sources: sources, log: log}, nil
}

// MemoryFile is one rendered output file. Paths use forward slashes and are
// always relative to the generated project root.
type MemoryFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// memoryPlan is the static half of a generation: functions, helpers and parsed
// templates. It depends only on bridge files, never on the domain model, so a
// single entry is enough for a Studio that edits the model but keeps the bridge.
type memoryPlan struct {
	funcMap  template.FuncMap
	helpers  *template.Template
	compiled map[string]*compiledSpec
}

// memoryPlanCache mirrors the bounded parsedSourceCache: one entry, cleared
// when the bridge chain changes.
var memoryPlanCache = struct {
	sync.Mutex
	key  string
	plan *memoryPlan
}{}

func (r *Renderer) memoryPlan() (*memoryPlan, error) {
	key := r.memoryPlanKey()
	memoryPlanCache.Lock()
	defer memoryPlanCache.Unlock()
	if memoryPlanCache.plan != nil && memoryPlanCache.key == key {
		return memoryPlanCache.plan, nil
	}

	funcMap, err := r.buildFuncMap()
	if err != nil {
		return nil, fmt.Errorf("build template functions: %w", err)
	}
	helpers, err := r.parseHelpers(funcMap)
	if err != nil {
		return nil, err
	}
	plan := &memoryPlan{funcMap: funcMap, helpers: helpers, compiled: make(map[string]*compiledSpec)}
	for _, src := range r.sources {
		for _, spec := range src.config.Templates {
			k := src.dir + "\x00" + spec.Source + "\x00" + strings.Join(spec.TargetPatterns(), "\x00")
			compiled, err := r.compileSpec(src, spec, funcMap, helpers)
			if err != nil {
				return nil, err
			}
			plan.compiled[k] = compiled
		}
	}
	memoryPlanCache.key, memoryPlanCache.plan = key, plan
	return plan, nil
}

func (r *Renderer) memoryPlanKey() string {
	var b strings.Builder
	left, right := r.delimiters()
	b.WriteString(left)
	b.WriteByte('\x00')
	b.WriteString(right)
	for _, src := range r.sources {
		b.WriteString(src.dir)
		b.WriteByte('\x00')
		b.WriteString(src.config.Helpers)
		b.WriteByte('\x00')
		for _, spec := range src.config.Templates {
			b.WriteString(spec.For)
			b.WriteByte(':')
			b.WriteString(spec.Source)
			b.WriteByte(':')
			b.WriteString(spec.When)
			b.WriteByte('\x00')
		}
	}
	return b.String()
}

// RenderMemory executes the same templates as Render but writes into memory.
// It deliberately has no filesystem, symlink or rollback machinery: in a
// browser there is no output directory, and the caller decides what to do with
// the returned files. Packages are supplied by the caller — resolving them
// from NuGet would require a network round trip from the browser.
func (r *Renderer) RenderMemory(project *ir.Project, pkgs map[string]string) ([]MemoryFile, error) {
	if project == nil {
		return nil, errors.New("ir project is nil")
	}
	plan, err := r.memoryPlan()
	if err != nil {
		return nil, err
	}

	files := make(map[string]string)
	customTargets := make(map[string]bool)
	for _, src := range r.sources {
		for _, spec := range src.config.Templates {
			if !r.shouldRender(spec, project, pkgs) {
				continue
			}
			k := src.dir + "\x00" + spec.Source + "\x00" + strings.Join(spec.TargetPatterns(), "\x00")
			compiled := plan.compiled[k]
			if compiled == nil {
				return nil, fmt.Errorf("no compiled template for %s", spec.Source)
			}

			contexts, err := r.renderContexts(spec.For, project, pkgs)
			if err != nil {
				return nil, err
			}
			for _, context := range contexts {
				if !r.shouldRenderContext(spec, context) {
					continue
				}
				for _, targetTpl := range compiled.targets {
					rendered, err := executeTemplateString(targetTpl, context)
					if err != nil {
						return nil, fmt.Errorf("render target path: %w", err)
					}
					// The same lexical containment rule as the disk renderer;
					// a browser has no symlinks, but `..` and volume names must
					// never escape the generated project root.
					_, rel, err := fsutil.LocalPathWithin(".", rendered)
					if err != nil || rel == "." {
						return nil, fmt.Errorf("template target path escapes output directory: %s", rendered)
					}
					rel = filepath.ToSlash(rel)
					if spec.IsCustom() {
						if customTargets[rel] {
							continue
						}
						customTargets[rel] = true
					}
					var buf strings.Builder
					buf.Grow(int(compiled.sizeHint.Load()))
					if err := compiled.body.Execute(&buf, context); err != nil {
						return nil, fmt.Errorf("execute template %s: %w", spec.Source, err)
					}
					compiled.sizeHint.Store(int64(buf.Len()))
					files[rel] = buf.String()
				}
			}
		}
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	out := make([]MemoryFile, 0, len(paths))
	for _, p := range paths {
		out = append(out, MemoryFile{Path: p, Content: files[p]})
	}
	return out, nil
}
