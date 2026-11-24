package bridge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

// TestCoreContractVersionMatchesSpecmeta pins the single source of truth: the
// bridge gate must move with specmeta.ContractVersion after a contract bump.
func TestCoreContractVersionMatchesSpecmeta(t *testing.T) {
	if CoreContractVersion != specmeta.ContractVersion {
		t.Errorf("CoreContractVersion = %d, specmeta.ContractVersion = %d", CoreContractVersion, specmeta.ContractVersion)
	}
	if err := shouldApplyContract(specmeta.ContractVersion); err != nil {
		t.Errorf("contract equal to specmeta version must be accepted: %v", err)
	}
	if err := shouldApplyContract(specmeta.ContractVersion + 1); err == nil {
		t.Error("contract newer than specmeta version must be rejected")
	}
}

func TestParseBridgeContractVersion(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "absent defaults to 1", in: "name: b\n", want: 1},
		{name: "explicit 1", in: "name: b\ncontract_version: 1\n", want: 1},
		{name: "explicit 2", in: "name: b\ncontract_version: 2\n", want: 2},
		{name: "malformed yaml is an error", in: "contract_version: [1, 2\n", wantErr: true},
		{name: "scalar manifest is an error", in: "just a string\n", wantErr: true},
		{name: "non integer is an error", in: "contract_version: two\n", wantErr: true},
		{name: "negative is an error", in: "contract_version: -1\n", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseBridgeContractVersion([]byte(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("parseBridgeContractVersion error = %v, wantErr %v", err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("parseBridgeContractVersion = %d, want %d", got, c.want)
			}
		})
	}
}

func TestShouldApplyContract(t *testing.T) {
	if err := shouldApplyContract(1); err != nil {
		t.Errorf("contract 1 should be accepted, got %v", err)
	}
	if err := shouldApplyContract(CoreContractVersion); err != nil {
		t.Errorf("contract equal to core should be accepted, got %v", err)
	}
	if err := shouldApplyContract(CoreContractVersion + 1); err == nil {
		t.Error("newer contract must be rejected")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error should mention the contract, got: %v", err)
	}
}

func TestRemoteContractVersion(t *testing.T) {
	orig := httpGetBodyFn
	defer func() { httpGetBodyFn = orig }()

	httpGetBodyFn = func(url string) ([]byte, error) {
		if !strings.Contains(url, "/bridge.yaml") {
			return nil, fmt.Errorf("unexpected url %s", url)
		}
		switch {
		case strings.Contains(url, "unsupported"):
			return []byte("name: b\ncontract_version: 2\n"), nil
		case strings.Contains(url, "malformed"):
			return []byte("contract_version: [1, 2\n"), nil
		default:
			return []byte("name: b\n"), nil
		}
	}

	ok := RegistryEntry{GitHub: "DomainCraft/domaincraft-bridge-csharp-core"}
	if v, err := remoteContractVersion(ok, "main"); err != nil || v != 1 {
		t.Errorf("supported contract = %d, %v; want 1", v, err)
	}

	bad := RegistryEntry{GitHub: "DomainCraft/unsupported"}
	if v, err := remoteContractVersion(bad, "main"); err != nil || v != 2 {
		t.Errorf("unsupported contract = %d, %v; want 2", v, err)
	}

	malformed := RegistryEntry{GitHub: "DomainCraft/malformed"}
	if _, err := remoteContractVersion(malformed, "main"); err == nil {
		t.Error("malformed remote manifest must be an error, not contract v1")
	}

	probeFailure := RegistryEntry{GitHub: "DomainCraft/offline"}
	origGet := httpGetBodyFn
	httpGetBodyFn = func(string) ([]byte, error) { return nil, fmt.Errorf("offline") }
	if _, err := remoteContractVersion(probeFailure, "main"); err == nil {
		t.Error("failed probe must be an error")
	}
	httpGetBodyFn = origGet
}
