package bridge

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseBridgeContractVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"absent defaults to 1", "name: b\n", 1},
		{"explicit 1", "name: b\ncontract_version: 1\n", 1},
		{"explicit 2", "name: b\ncontract_version: 2\n", 2},
		{"garbage defaults to 1", "::: not yaml", 1},
	}
	for _, c := range cases {
		if got := parseBridgeContractVersion([]byte(c.in)); got != c.want {
			t.Errorf("%s: parseBridgeContractVersion = %d, want %d", c.name, got, c.want)
		}
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
		if strings.Contains(url, "unsupported") {
			return []byte("name: b\ncontract_version: 2\n"), nil
		}
		return []byte("name: b\n"), nil
	}

	ok := RegistryEntry{GitHub: "DomainCraft/domaincraft-bridge-csharp-core"}
	if v, err := remoteContractVersion(ok, "main"); err != nil || v != 1 {
		t.Errorf("supported contract = %d, %v; want 1", v, err)
	}

	bad := RegistryEntry{GitHub: "DomainCraft/unsupported"}
	if v, err := remoteContractVersion(bad, "main"); err != nil || v != 2 {
		t.Errorf("unsupported contract = %d, %v; want 2", v, err)
	}
}
