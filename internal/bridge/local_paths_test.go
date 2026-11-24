package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localBridgePathsFixtures lays out the four inputs shared by the normalization
// tests: a valid bridge directory, a directory without a config, the config file
// itself and a path that does not exist.
func localBridgePathsFixtures(t *testing.T) (withConfig, withoutConfig, configFile, missing string) {
	t.Helper()
	root := t.TempDir()
	withConfig = filepath.Join(root, "with-config")
	if err := os.MkdirAll(withConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile = filepath.Join(withConfig, "bridge.yaml")
	if err := os.WriteFile(configFile, []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withoutConfig = filepath.Join(root, "without-config")
	if err := os.MkdirAll(withoutConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	missing = filepath.Join(root, "missing")
	return withConfig, withoutConfig, configFile, missing
}

func TestLocalBridgePaths(t *testing.T) {
	t.Parallel()
	withConfig, withoutConfig, configFile, missing := localBridgePathsFixtures(t)

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
			dir, config, err := LocalBridgePaths(tc.input)
			if tc.wantErrPart != "" {
				if err == nil {
					t.Fatalf("LocalBridgePaths(%q) = (%q, %q), want error containing %q", tc.input, dir, config, tc.wantErrPart)
				}
				if !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("LocalBridgePaths(%q): %v", tc.input, err)
			}
			if dir != tc.wantDir || config != tc.wantConfig {
				t.Errorf("LocalBridgePaths(%q) = (%q, %q), want (%q, %q)", tc.input, dir, config, tc.wantDir, tc.wantConfig)
			}
			// The config path returned for a valid input must exist, not be a
			// re-derived lookalike.
			if _, err := os.Stat(config); err != nil {
				t.Errorf("returned config %q is not readable: %v", config, err)
			}
		})
	}
}

// Resolver.Resolve must keep its original diagnostics: a directory without a
// config is a hard error, a missing path-like input never becomes a clone, and
// a missing bare name still falls through to registry/owner-repo resolution.
func TestResolver_LocalPathNormalization(t *testing.T) {
	t.Parallel()
	withConfig, withoutConfig, configFile, missing := localBridgePathsFixtures(t)
	r := NewResolver(&Registry{})

	cases := []struct {
		name        string
		input       string
		wantDir     string
		wantErrPart string
	}{
		{name: "existing directory with bridge.yaml", input: withConfig, wantDir: withConfig},
		{name: "existing directory without bridge.yaml", input: withoutConfig, wantErrPart: "does not contain bridge.yaml"},
		{name: "file path", input: configFile, wantDir: withConfig},
		{name: "missing path-like input", input: missing, wantErrPart: "no such local path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, err := r.Resolve(tc.input)
			if tc.wantErrPart != "" {
				if err == nil {
					t.Fatalf("Resolve(%q) = %q, want error containing %q", tc.input, dir, tc.wantErrPart)
				}
				if !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErrPart)
				}
				if strings.Contains(err.Error(), "github.com") {
					t.Fatalf("Resolve(%q) fell through to a GitHub clone: %v", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.input, err)
			}
			if dir != tc.wantDir {
				t.Errorf("Resolve(%q) = %q, want %q", tc.input, dir, tc.wantDir)
			}
		})
	}
}
