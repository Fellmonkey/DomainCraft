//go:build js && wasm

package renderer

import "github.com/DomainCraft/DomainCraft/internal/ir"

// resolvePackages is a no-op in the browser: NuGet resolution needs HTTP and a
// filesystem cache. The caller passes package versions to RenderMemory, and
// templates without version references render unchanged.
func (r *Renderer) resolvePackages(_ *ir.Project) map[string]string {
	return nil
}
