// Pure WASM API: no syscall/js here, so the exact export logic can be tested
// and benchmarked natively. main.go is only the JS glue.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	domaincraft "github.com/DomainCraft/DomainCraft"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/lexer"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/seed"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/validator"
	"go.yaml.in/yaml/v3"
)

type validationError struct {
	Entity  string `json:"entity,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Warning bool   `json:"warning"`
}

type validationResult struct {
	Errors []validationError `json:"errors"`
	Parse  bool              `json:"parse"`
}

// resultCache memoizes the last (input, JSON) pair. Studio re-runs validation
// on an unchanged model, so the second call is free. Only strings are retained:
// an earlier cache of *parser.RawSchema kept a large object graph alive and
// raised GC pressure for every other WASM export.
type resultCache struct {
	mu    sync.Mutex
	valid bool
	key   string
	value string
}

func (c *resultCache) get(key string, compute func() string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && c.key == key {
		return c.value
	}
	value := compute()
	c.valid, c.key, c.value = true, key, value
	return value
}

var (
	validateCache resultCache
	domainCache   resultCache
)

// ValidationErrorResult is the JS-input-contract result for goValidate.
func ValidationErrorResult(message string) string {
	return marshal(validationResult{
		Errors: []validationError{{Message: message}},
		Parse:  true,
	})
}

// FieldErrorResult is the JS-input-contract result for goParseField and
// goParseDomain.
func FieldErrorResult(message string) string {
	return marshal(map[string]string{"error": message})
}

// parseAndValidateYAML is shared by goValidate and goGenerate: the CLI never
// renders a schema that the validator rejected, and the browser must not either.
func parseAndValidateYAML(yaml string) (*parser.ParsedSchema, []validator.ValidationError, error) {
	schema, err := parser.ParseYAMLString(yaml)
	if err != nil {
		return nil, nil, err
	}
	return schema, validator.New(schema).Validate(), nil
}

// ValidateYAML is the production path: main.go converts the js.Value once and
// calls this function directly, without a temporary []string.
func ValidateYAML(yaml string) string {
	return validateCache.get(yaml, func() string {
		_, errs, err := parseAndValidateYAML(yaml)
		if err != nil {
			return marshal(validationResult{
				Errors: []validationError{{Message: err.Error()}},
				Parse:  true,
			})
		}
		result := make([]validationError, 0, len(errs))
		for _, e := range errs {
			result = append(result, validationError{
				Entity:  e.Entity,
				Field:   e.Field,
				Message: e.Error(),
				Warning: e.Warning,
			})
		}
		return marshal(validationResult{Errors: result})
	})
}

// ParseFieldDefinition is the production path for goParseField.
func ParseFieldDefinition(def, name string) string {
	fd, err := lexer.ParseFieldString(def)
	if err != nil {
		return marshal(map[string]string{"error": err.Error()})
	}
	fd.Name = name
	// The Studio bridge expects Validations to be an object, never null.
	if fd.Validations == nil {
		fd.Validations = make(map[string]string)
	}
	return marshalFieldDefinition(fd)
}

// ParseDomainYAML is the production path for goParseDomain.
func ParseDomainYAML(yaml string) string {
	return domainCache.get(yaml, func() string {
		raw, err := parser.ParseRawSchemaString(yaml)
		if err != nil {
			return marshal(map[string]string{"error": err.Error()})
		}
		return marshal(raw)
	})
}

// specmetaJSON is computed once: the constants are static, but marshaling the
// whole table on every call cost ~41 us and 160 allocations in the benchmark.
var specmetaJSON = sync.OnceValue(func() string {
	return marshal(specmeta.JSON())
})

// Specmeta mirrors window.goSpecmeta(): the full spec constant table as JSON.
func Specmeta() string {
	return specmetaJSON()
}

// Version mirrors window.goVersion(): the version stamped by -ldflags.
func Version(v string) string {
	return v
}

// generateResult is the JSON returned by window.goGenerate. Files are ordered
// by path so the same model always produces the same bytes.
type generateResult struct {
	Files []renderer.MemoryFile `json:"files"`
	Error string                `json:"error,omitempty"`
}

// generationPlan holds the static half of a generation: the resolved chain and
// a renderer with compiled templates. Both depend only on the embedded bridges
// tree, never on the user's model, so one entry is enough for the Studio
// pattern (many edits, same bridge).
type generationPlan struct {
	renderer *renderer.Renderer
}

var generationPlanCache = struct {
	sync.Mutex
	ref  string
	plan *generationPlan
}{}

func generationPlanFor(ref string) (*generationPlan, error) {
	generationPlanCache.Lock()
	defer generationPlanCache.Unlock()
	if generationPlanCache.plan != nil && generationPlanCache.ref == ref {
		return generationPlanCache.plan, nil
	}
	chain, err := embeddedBridgeChain(ref)
	if err != nil {
		return nil, err
	}
	r, err := renderer.NewFromFS(domaincraft.Bridges, chain, nil)
	if err != nil {
		return nil, err
	}
	plan := &generationPlan{renderer: r}
	generationPlanCache.ref, generationPlanCache.plan = ref, plan
	return plan, nil
}

// GenerateYAML compiles domain.yaml into files in memory. bridgeRef is a
// registry ID of a bundled bridge (or a path inside the embedded bridges tree).
// packagesJSON is an optional map of package versions; the browser cannot
// resolve NuGet versions without a network round trip, so the caller supplies
// them when it has them.
func GenerateYAML(yamlText, bridgeRef, packagesJSON string) string {
	result := generateResult{Files: []renderer.MemoryFile{}}
	plan, err := generationPlanFor(bridgeRef)
	if err != nil {
		result.Error = err.Error()
		return marshal(result)
	}
	schema, errs, err := parseAndValidateYAML(yamlText)
	if err != nil {
		result.Error = err.Error()
		return marshal(result)
	}
	hard := make([]error, 0, len(errs))
	for _, e := range errs {
		if !e.Warning {
			hard = append(hard, e)
		}
	}
	if len(hard) > 0 {
		result.Error = fmt.Sprintf("validation failed with %d error(s): %v", len(hard), errors.Join(hard...))
		return marshal(result)
	}
	project, err := ir.Build(schema)
	if err != nil {
		result.Error = err.Error()
		return marshal(result)
	}

	var packages map[string]string
	if strings.TrimSpace(packagesJSON) != "" {
		if err := json.Unmarshal([]byte(packagesJSON), &packages); err != nil {
			result.Error = "invalid packages JSON: " + err.Error()
			return marshal(result)
		}
	}

	r := plan.renderer
	r.SetSeedData(seed.Normalize(project))
	r.SetMockData(seed.Generate(project, seed.Options{}))
	files, err := r.RenderMemory(project, packages)
	if err != nil {
		result.Error = err.Error()
		return marshal(result)
	}
	result.Files = files
	return marshal(result)
}

// embeddedBridgeChain resolves an extends chain entirely inside the embedded
// bridge tree. No clone, cache or network access exists in the browser.
func embeddedBridgeChain(ref string) ([]string, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("no bridge provided")
	}
	seen := make(map[string]bool)
	chain := make([]string, 0, 4)
	for ref != "" {
		dir, err := findEmbeddedBridge(ref)
		if err != nil {
			return nil, err
		}
		if seen[dir] {
			return nil, fmt.Errorf("cyclic bridge chain at %q", ref)
		}
		seen[dir] = true
		manifest, err := fs.ReadFile(domaincraft.Bridges, path.Join(dir, "bridge.yaml"))
		if err != nil {
			return nil, fmt.Errorf("read bridge %s: %w", dir, err)
		}
		var meta struct {
			Extends string `yaml:"extends"`
		}
		if err := yaml.Unmarshal(manifest, &meta); err != nil {
			return nil, fmt.Errorf("parse bridge %s: %w", dir, err)
		}
		chain = append(chain, dir)
		ref = meta.Extends
	}
	slices.Reverse(chain)
	return chain, nil
}

// findEmbeddedBridge resolves a registry ID (the bridge.yaml name) or a path
// inside the embedded tree. Matching by name avoids importing the registry
// package, which pulls the network-backed clone/update code into the WASM
// binary.
func findEmbeddedBridge(ref string) (string, error) {
	clean := path.Clean(filepath.ToSlash(ref))
	candidates := []string{path.Join("bridges", clean)}
	if strings.HasPrefix(clean, "bridges/") {
		candidates = append(candidates, clean)
	}
	for _, dir := range candidates {
		if path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") {
			continue
		}
		if _, err := fs.Stat(domaincraft.Bridges, path.Join(dir, "bridge.yaml")); err == nil {
			return dir, nil
		}
	}
	var found string
	err := fs.WalkDir(domaincraft.Bridges, "bridges", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "bridge.yaml" {
			return nil
		}
		data, err := fs.ReadFile(domaincraft.Bridges, p)
		if err != nil {
			return nil
		}
		var meta struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(data, &meta); err == nil && meta.Name == ref {
			found = path.Dir(p)
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("unknown bridge %q", ref)
	}
	return found, nil
}

func marshal(v any) string {
	var b strings.Builder
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		data, _ := json.Marshal(map[string]string{"error": "marshal failed: " + err.Error()})
		return string(data)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
