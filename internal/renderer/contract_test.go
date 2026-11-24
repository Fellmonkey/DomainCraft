package renderer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

func writeBridgeConfig(t *testing.T, yamlContent string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadSourceRejectsNewerContract(t *testing.T) {
	t.Parallel()
	dir := writeBridgeConfig(t, "name: b\ncontract_version: 99\n")
	_, err := loadSource(dir)
	if err == nil {
		t.Fatal("expected an error for a bridge declaring a newer contract")
	}
}

func TestLoadSourceDefaultsAbsentContractTo1(t *testing.T) {
	t.Parallel()
	dir := writeBridgeConfig(t, "name: b\n")
	src, err := loadSource(dir)
	if err != nil {
		t.Fatalf("loadSource: %v", err)
	}
	if src.config.ContractVersion != 1 {
		t.Errorf("contract_version = %d, want default 1", src.config.ContractVersion)
	}
	if src.contractDeclared {
		t.Error("contractDeclared = true, want false (field was absent)")
	}
}

func TestLoadSourceAcceptsMatchingContract(t *testing.T) {
	t.Parallel()
	dir := writeBridgeConfig(t, "name: b\ncontract_version: 1\n")
	src, err := loadSource(dir)
	if err != nil {
		t.Fatalf("loadSource: %v", err)
	}
	if src.config.ContractVersion != 1 {
		t.Errorf("contract_version = %d, want 1", src.config.ContractVersion)
	}
	if !src.contractDeclared {
		t.Error("contractDeclared = false, want true (field was present)")
	}
}

func TestNewWarnsWhenContractAbsent(t *testing.T) {
	t.Parallel()
	dir := writeBridgeConfig(t, "name: b\n")
	var buf bytes.Buffer
	log := logger.New()
	log.SetWriter(&buf)
	if _, err := New(dir, log); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(buf.String(), "contract_version") {
		t.Errorf("expected a deprecation warning for missing contract_version, got: %q", buf.String())
	}
}
