package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDomainLock(t *testing.T) {
	root := t.TempDir()
	// The bridge package recognises a monorepo by its module marker.
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module github.com/DomainCraft/DomainCraft\n")

	// A monorepo-located bridge (csharp-core → bridges/csharp/core).
	mp := filepath.Join(root, "bridges", "csharp", "core")
	mustWriteFile(t, filepath.Join(mp, "bridge.yaml"),
		"name: csharp-core\nversion: \"1.2.3\"\ncontract_version: 2\n")

	// An arbitrary local bridge (no registry entry, no monorepo location).
	local := filepath.Join(root, "my-bridge")
	mustWriteFile(t, filepath.Join(local, "bridge.yaml"), "name: my-bridge\nversion: \"0.1.0\"\n")

	domain := filepath.Join(root, "domain.yaml")
	mustWriteFile(t, domain, "name: demo\n")

	if err := writeDomainLock(domain, []string{local, mp}); err != nil {
		t.Fatalf("writeDomainLock: %v", err)
	}

	lockPath := filepath.Join(root, "domaincraft.lock.json")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}

	var lock domainLock
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("unmarshal lock: %v", err)
	}

	if len(lock.Bridges) != 2 {
		t.Fatalf("bridges = %d, want 2 (%v)", len(lock.Bridges), lock.Bridges)
	}

	// Sorted by ID: csharp-core before my-bridge.
	first := lock.Bridges[0]
	if first.ID != "csharp-core" {
		t.Fatalf("first bridge = %q, want csharp-core", first.ID)
	}
	if first.Path != "bridges/csharp/core" {
		t.Errorf("csharp-core path = %q, want bridges/csharp/core", first.Path)
	}
	if first.Source != "" {
		t.Errorf("csharp-core source = %q, want empty (monorepo)", first.Source)
	}
	if first.Version != "1.2.3" {
		t.Errorf("csharp-core version = %q, want 1.2.3", first.Version)
	}
	if first.ContractVersion != 2 {
		t.Errorf("csharp-core contract_version = %d, want 2", first.ContractVersion)
	}

	second := lock.Bridges[1]
	if second.ID != "my-bridge" {
		t.Fatalf("second bridge = %q, want my-bridge", second.ID)
	}
	if second.Path != "" || second.Source != "" {
		t.Errorf("my-bridge should have neither path nor source: %+v", second)
	}
	if second.ContractVersion != 1 {
		t.Errorf("my-bridge contract_version = %d, want default 1", second.ContractVersion)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
