package renderer

import (
	"strings"
	"testing"
)

// TestContractVersionMatrix is the compatibility oracle for bridge contracts:
// absent → 1 (accepted, deprecated); equal → accepted; newer → rejected whose a
// hard error naming both versions. This is the core guard against silent
// incompatibility between a bridge and the core.
func TestContractVersionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantOK  bool
		wantVer int
	}{
		{"absent defaults to 1", "name: b\n", true, 1},
		{"explicit 1 accepted", "name: b\ncontract_version: 1\n", true, 1},
		{"newer 2 rejected", "name: b\ncontract_version: 2\n", false, 0},
		{"much newer 9 rejected", "name: b\ncontract_version: 9\n", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
	base := writeBridgeConfig(t, "name: base\ncontract_version: 2\n")
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
	base := writeBridgeConfig(t, "name: base\ncontract_version: 1\n")
	adapter := writeBridgeConfig(t, "name: adapter\nextends: "+base+"\n")

	r, err := New(adapter, nil)
	if err != nil {
		t.Fatalf("New(adapter): %v", err)
	}
	if err := r.AttachBases([]string{base}); err != nil {
		t.Fatalf("AttachBases: %v", err)
	}
}
