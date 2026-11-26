package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBridgeMetadataRejectsNegativeContractVersion keeps the lock-file reader
// on the same contract parser as the renderer and update gate. A local int
// field used to accept a negative version that the shared parser rejects.
func TestBridgeMetadataRejectsNegativeContractVersion(t *testing.T) {
	dir := t.TempDir()
	manifest := "name: bad\nversion: 1.0.0\ncontract_version: -1\n"
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write bridge.yaml: %v", err)
	}
	if _, _, _, err := bridgeMetadata(dir); err == nil {
		t.Fatal("bridgeMetadata accepted a negative contract_version")
	}
}
