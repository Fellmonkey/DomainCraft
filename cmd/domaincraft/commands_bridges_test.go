package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

// TestBridgesCheckUpdatesKeepsRegistryOrder pins the output contract of the
// concurrent probe: every cached bridge is checked in parallel, but rows must
// still follow the registry order.
func TestBridgesCheckUpdatesKeepsRegistryOrder(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"bridges", "--check-updates"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	entries := bridge.Default().All()
	if len(lines) < len(entries)+2 {
		t.Fatalf("got %d output lines, want at least %d", len(lines), len(entries)+2)
	}
	for i, e := range entries {
		want := fmt.Sprintf("%-20s", e.ID)
		if got := lines[i+2]; !strings.HasPrefix(got, want) {
			t.Errorf("row %d = %q, want registry entry %q", i, got, e.ID)
		}
	}
}
