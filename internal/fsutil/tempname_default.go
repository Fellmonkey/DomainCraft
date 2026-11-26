//go:build !js || !wasm

package fsutil

import "crypto/rand"

// atomicTempSuffix keeps a random collision-free suffix out of the shared
// fsutil file so the WASM build does not link crypto/rand for code it never
// calls (RenderMemory has no disk writes).
func atomicTempSuffix() string {
	return rand.Text()
}
