//go:build js && wasm

package fsutil

// atomicTempSuffix is unreachable in the browser: the in-memory renderer
// never creates temp files. Keeping it constant avoids pulling crypto/rand
// into the WASM binary.
func atomicTempSuffix() string {
	return "mem"
}
