//go:build !js || !wasm

package renderer

import (
	"maps"
	"slices"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/packages"
)

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

// resolvePackages resolves all package versions from the package registry,
// cached per bridge so repeated runs don't hit the registry every time.
// Registry packages from the chain are merged base-first, adapter wins.
func (r *Renderer) resolvePackages(project *ir.Project) map[string]string {
	framework := r.targetFramework(project)

	merged := make(map[string]string)
	for _, src := range r.sources {
		maps.Copy(merged, src.config.RegistryPackages)
	}
	if len(merged) == 0 {
		return nil
	}

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
			r.log.Warn("failed to resolve package %s: %v", packageID, err)
		}
		if version != "" {
			result[key] = version
		}
	}
	return result
}
