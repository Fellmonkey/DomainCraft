//go:build js && wasm

package renderer

import (
	"path"
	"path/filepath"
)

// The browser never calls loadSource (bridges come from the embedded fs.FS),
// but the symbol must exist so disk and WASM builds share load.go.
func localBridgePaths(input string) (string, string, error) {
	clean := filepath.ToSlash(filepath.Clean(input))
	return clean, path.Join(clean, "bridge.yaml"), nil
}
