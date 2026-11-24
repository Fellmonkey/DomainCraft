package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveBridgePath covers the four local-path shapes through the shared
// bridge.LocalBridgePaths normalization. The renderer used to accept a missing
// path as a config file and failed only on read; it must now fail at resolution.
func TestResolveBridgePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	withConfig := filepath.Join(root, "with-config")
	if err := os.MkdirAll(withConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(withConfig, "bridge.yaml")
	if err := os.WriteFile(configFile, []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withoutConfig := filepath.Join(root, "without-config")
	if err := os.MkdirAll(withoutConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")

	cases := []struct {
		name        string
		input       string
		wantDir     string
		wantConfig  string
		wantErrPart string
	}{
		{name: "existing directory with bridge.yaml", input: withConfig, wantDir: withConfig, wantConfig: configFile},
		{name: "existing directory without bridge.yaml", input: withoutConfig, wantErrPart: "does not contain bridge.yaml"},
		{name: "file path", input: configFile, wantDir: withConfig, wantConfig: configFile},
		{name: "missing path", input: missing, wantErrPart: "bridge path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, config, err := resolveBridgePath(tc.input)
			if tc.wantErrPart != "" {
				if err == nil {
					t.Fatalf("resolveBridgePath(%q) = (%q, %q), want error containing %q", tc.input, dir, config, tc.wantErrPart)
				}
				if !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBridgePath(%q): %v", tc.input, err)
			}
			if dir != tc.wantDir || config != tc.wantConfig {
				t.Errorf("resolveBridgePath(%q) = (%q, %q), want (%q, %q)", tc.input, dir, config, tc.wantDir, tc.wantConfig)
			}
		})
	}
}

func TestLoadSourceFailsClosedOnMissingBridgeYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := loadSource(dir); err == nil || !strings.Contains(err.Error(), "does not contain bridge.yaml") {
		t.Fatalf("loadSource on a directory without bridge.yaml = %v, want a hard error naming bridge.yaml", err)
	}
}
