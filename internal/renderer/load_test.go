package renderer

import (
	"os"
	"path/filepath"
	"slices"
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

// TestLoadSourceCacheFollowsContentChanges pins the shared parse cache: a
// second load of an unchanged manifest reuses the parsed config, while a bridge
// refreshed mid-command (--update-bridges or a manual edit) is parsed anew.
func TestLoadSourceCacheFollowsContentChanges(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "bridge.yaml")
	writeManifest := func(name string) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte("name: "+name+"\ncontract_version: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeManifest("first")
	for i := range 2 {
		src, err := loadSource(dir)
		if err != nil {
			t.Fatal(err)
		}
		if src.config.Name != "first" {
			t.Fatalf("load %d: name = %q, want first", i, src.config.Name)
		}
	}

	writeManifest("second")
	src, err := loadSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src.config.Name != "second" {
		t.Fatalf("name = %q, want second after the file changed", src.config.Name)
	}
}

func TestLoadSourceFailsClosedOnMissingBridgeYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := loadSource(dir); err == nil || !strings.Contains(err.Error(), "does not contain bridge.yaml") {
		t.Fatalf("loadSource on a directory without bridge.yaml = %v, want a hard error naming bridge.yaml", err)
	}
}

// MigrationConfig scans the composed chain from the adapter downwards and
// returns the first *enabled* config. The adapter wins when both layers enable
// migrations; a disabled adapter defers to an enabled persistence base.
func TestMigrationConfigFirstEnabledWins(t *testing.T) {
	t.Parallel()
	baseEnabled := writeBridgeConfig(t, `name: base
migrations:
  enabled: true
  commands:
    - "base command"
`)
	baseDisabled := writeBridgeConfig(t, `name: base
migrations:
  enabled: false
  commands:
    - "base command"
`)
	adapterEnabled := writeBridgeConfig(t, `name: adapter
migrations:
  enabled: true
  commands:
    - "adapter command"
`)
	adapterDisabled := writeBridgeConfig(t, `name: adapter
migrations:
  enabled: false
  commands:
    - "adapter command"
`)

	cases := []struct {
		name         string
		base         string
		adapter      string
		wantSource   int // index in r.sources: 0 = base, 1 = adapter
		wantCommands []string
	}{
		{name: "both enabled: adapter wins", base: baseEnabled, adapter: adapterEnabled, wantSource: 1, wantCommands: []string{"adapter command"}},
		{name: "adapter disabled: enabled base wins", base: baseEnabled, adapter: adapterDisabled, wantSource: 0, wantCommands: []string{"base command"}},
		{name: "base disabled: enabled adapter wins", base: baseDisabled, adapter: adapterEnabled, wantSource: 1, wantCommands: []string{"adapter command"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := New(tc.adapter, nil)
			if err != nil {
				t.Fatalf("New(adapter): %v", err)
			}
			if err := r.AttachBases([]string{tc.base}); err != nil {
				t.Fatalf("AttachBases: %v", err)
			}
			got := r.MigrationConfig()
			if got == nil {
				t.Fatal("MigrationConfig() = nil, want an enabled config")
			}
			if !got.Enabled {
				t.Error("MigrationConfig() returned a disabled config")
			}
			if got != r.sources[tc.wantSource].config.Migrations {
				t.Errorf("MigrationConfig() did not come from source %d", tc.wantSource)
			}
			if !slices.Equal(got.Commands, tc.wantCommands) {
				t.Errorf("commands = %v, want %v", got.Commands, tc.wantCommands)
			}
		})
	}
}

func TestMigrationConfigAbsentReturnsNil(t *testing.T) {
	t.Parallel()
	t.Run("no bridge declares migrations", func(t *testing.T) {
		t.Parallel()
		base := writeBridgeConfig(t, "name: base\n")
		adapter := writeBridgeConfig(t, "name: adapter\n")
		r, err := New(adapter, nil)
		if err != nil {
			t.Fatalf("New(adapter): %v", err)
		}
		if err := r.AttachBases([]string{base}); err != nil {
			t.Fatalf("AttachBases: %v", err)
		}
		if got := r.MigrationConfig(); got != nil {
			t.Errorf("MigrationConfig() = %+v, want nil", got)
		}
	})
	t.Run("all declared configs disabled", func(t *testing.T) {
		t.Parallel()
		base := writeBridgeConfig(t, "name: base\nmigrations:\n  enabled: false\n  commands:\n    - \"never\"\n")
		adapter := writeBridgeConfig(t, "name: adapter\nmigrations:\n  enabled: false\n  commands:\n    - \"never\"\n")
		r, err := New(adapter, nil)
		if err != nil {
			t.Fatalf("New(adapter): %v", err)
		}
		if err := r.AttachBases([]string{base}); err != nil {
			t.Fatalf("AttachBases: %v", err)
		}
		if got := r.MigrationConfig(); got != nil {
			t.Errorf("MigrationConfig() = %+v, want nil when no config is enabled", got)
		}
	})
}

func TestMigrationConfigMergesCommandsInDeclaredOrder(t *testing.T) {
	t.Parallel()
	want := []string{
		"dotnet ef migrations add Sync",
		"dotnet ef database update",
		"echo done",
	}
	dir := writeBridgeConfig(t, `name: persistence
migrations:
  enabled: true
  commands:
    - "dotnet ef migrations add Sync"
    - "dotnet ef database update"
    - "echo done"
`)
	r, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := r.MigrationConfig()
	if got == nil {
		t.Fatal("MigrationConfig() = nil, want an enabled config")
	}
	if !got.Enabled {
		t.Error("MigrationConfig().Enabled = false, want true")
	}
	if !slices.Equal(got.Commands, want) {
		t.Errorf("commands = %v, want declared order %v", got.Commands, want)
	}
}
