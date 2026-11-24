// Package bridgechain resolves a bridge's `extends` chain into local bridge
// directories. It lives outside cmd/domaincraft so both the CLI and the
// cross-process contract tests walk the exact same chain.
package bridgechain

import (
	"fmt"
	"path/filepath"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

// Walk resolves ref relative to fromDir and follows `extends` to the deepest
// base. It returns absolute paths top-first: the resolved bridge first, then
// its bases. Absolute paths make cycle detection path-based, not textual.
func Walk(ref, fromDir string, resolver *bridge.Resolver, log *logger.Logger) ([]string, error) {
	var chain []string
	visited := make(map[string]bool)
	for ref != "" {
		path, err := bridge.ResolveExtendsRef(ref, fromDir, resolver)
		if err != nil {
			return nil, fmt.Errorf("resolve bridge `extends: %s`: %w", ref, err)
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if visited[path] {
			return nil, fmt.Errorf("bridge `extends` cycle detected at %q", ref)
		}
		visited[path] = true
		chain = append(chain, path)

		base, err := renderer.New(path, log)
		if err != nil {
			return nil, fmt.Errorf("load bridge %s: %w", path, err)
		}
		ref, fromDir = base.Extends(), path
	}
	return chain, nil
}
