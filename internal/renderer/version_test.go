package renderer

import (
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

// TestContractVersionAliasesSpecmeta pins the single source of truth: a bridge
// that omits `contract_version` resolves to the specmeta contract, never a copy.
func TestContractVersionAliasesSpecmeta(t *testing.T) {
	t.Parallel()
	dir := writeBridgeConfig(t, "name: b\n")
	r, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := r.Config().ContractVersion; got != specmeta.ContractVersion {
		t.Fatalf("default contract version = %d, specmeta.ContractVersion = %d", got, specmeta.ContractVersion)
	}
}

// TestContractVersionMatrix is the compatibility oracle for bridge contracts:
// absent → the core version (accepted); older and equal → accepted; newer →
// rejected with a hard error naming both versions. This is the core guard
// against silent incompatibility between a bridge and the core.
func TestContractVersionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		yaml    string
		wantOK  bool
		wantVer int
	}{
		{"absent defaults to current", "name: b\n", true, specmeta.ContractVersion},
		{"older 1 accepted", "name: b\ncontract_version: 1\n", true, 1},
		{"explicit current accepted", "name: b\ncontract_version: 2\n", true, 2},
		{"newer 3 rejected", "name: b\ncontract_version: 3\n", false, 0},
		{"much newer 9 rejected", "name: b\ncontract_version: 9\n", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := writeBridgeConfig(t, c.yaml)
			r, err := New(dir, nil)
			if c.wantOK {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if got := r.Config().ContractVersion; got != c.wantVer {
					t.Errorf("ContractVersion = %d, want %d", got, c.wantVer)
				}
			} else {
				if err == nil {
					t.Fatal("expected rejection of a newer contract")
				}
				if !strings.Contains(err.Error(), "contract") {
					t.Errorf("error should mention contract compatibility: %v", err)
				}
			}
		})
	}
}

// TestBridgeVersionParsed ensures the bridge `version:` field (independent per
// bridge) is parsed and surfaced — it feeds the lock file and version reporting.
func TestBridgeVersionParsed(t *testing.T) {
	t.Parallel()
	dir := writeBridgeConfig(t, "name: b\nversion: \"2.3.4\"\n")
	r, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := r.Config().Version; got != "2.3.4" {
		t.Errorf("Version = %q, want 2.3.4", got)
	}
}

// TestNewerContractRejectedInBase asserts the contract gate propagates through a
// composed chain: an adapter that is fine must still fail when a base bridge
// declares a newer contract.
func TestNewerContractRejectedInBase(t *testing.T) {
	t.Parallel()
	base := writeBridgeConfig(t, "name: base\ncontract_version: 3\n")
	adapter := writeBridgeConfig(t, "name: adapter\nextends: "+base+"\n")

	r, err := New(adapter, nil)
	if err != nil {
		t.Fatalf("New(adapter): %v", err)
	}
	if err := r.AttachBases([]string{base}); err == nil {
		t.Fatal("expected AttachBases to reject a base with a newer contract")
	}
}

// TestMatchingContractAcceptedInBase is the positive control: an equal-contract
// base attaches cleanly.
func TestMatchingContractAcceptedInBase(t *testing.T) {
	t.Parallel()
	base := writeBridgeConfig(t, "name: base\ncontract_version: 2\n")
	adapter := writeBridgeConfig(t, "name: adapter\nextends: "+base+"\n")

	r, err := New(adapter, nil)
	if err != nil {
		t.Fatalf("New(adapter): %v", err)
	}
	if err := r.AttachBases([]string{base}); err != nil {
		t.Fatalf("AttachBases: %v", err)
	}
}
