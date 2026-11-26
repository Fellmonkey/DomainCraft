package bridgecontract

import (
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

func TestParseContractVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		manifest  string
		want      int
		declared  bool
		wantError bool
	}{
		{name: "absent", manifest: "name: demo\n", want: specmeta.ContractVersion},
		{name: "explicit zero", manifest: "contract_version: 0\n", want: specmeta.ContractVersion},
		{name: "matching", manifest: "contract_version: 2\n", want: 2, declared: true},
		{name: "negative", manifest: "contract_version: -1\n", wantError: true},
		{name: "newer", manifest: "contract_version: 99\n", want: 99, declared: true},
		{name: "not a mapping", manifest: "- a\n", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, declared, err := ParseContractVersion([]byte(tt.manifest))
			if tt.wantError {
				if err == nil {
					t.Fatal("ParseContractVersion() accepted a bad manifest")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || declared != tt.declared {
				t.Fatalf("ParseContractVersion() = (%d, %v), want (%d, %v)", got, declared, tt.want, tt.declared)
			}
			if err := CheckContractVersion(got); (err != nil) != (got > specmeta.ContractVersion) {
				t.Fatalf("CheckContractVersion(%d) = %v", got, err)
			}
		})
	}
}

func TestCheckContractVersionText(t *testing.T) {
	t.Parallel()
	err := CheckContractVersion(specmeta.ContractVersion + 1)
	if err == nil || !strings.Contains(err.Error(), "requires contract") {
		t.Fatalf("CheckContractVersion() = %v", err)
	}
}
