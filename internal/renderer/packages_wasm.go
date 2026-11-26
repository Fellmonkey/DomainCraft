//go:build js && wasm

package renderer

import (
	"slices"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

// targetFramework mirrors the CLI build. Kept here so the in-browser renderer
// shares the same rule without importing the network-backed packages resolver.
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

// resolvePackages is a no-op in the browser: NuGet resolution needs HTTP and a
// filesystem cache. The caller passes package versions to RenderMemory, and
// templates without version references render unchanged.
func (r *Renderer) resolvePackages(_ *ir.Project) map[string]string {
	return nil
}
