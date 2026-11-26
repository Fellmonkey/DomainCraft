package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

func writeAdminTestBridge(t *testing.T, dir, template, target string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: " + filepath.Base(dir) + "\nversion: \"1.0.0\"\noutput_dir: generated\ntemplates:\n  - for: project\n    source: templates/main.tmpl\n    target: \"" + target + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bridge.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "main.tmpl"), []byte(template+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGenerateAdminPanelPath covers the documented --admin flow: a local admin
// bridge resolves, renders and contributes files to the same output tree.
func TestGenerateAdminPanelPath(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	root := t.TempDir()
	domainPath := filepath.Join(root, "domain.yaml")
	domain := "project:\n  name: Fixture\ndatabase: postgresql\nentities:\n  Item:\n    fields:\n      id: uuid [primary]\n"
	if err := os.WriteFile(domainPath, []byte(domain), 0o644); err != nil {
		t.Fatal(err)
	}
	mainBridge := writeAdminTestBridge(t, filepath.Join(root, "main-bridge"), "main", "main.txt")
	adminBridge := writeAdminTestBridge(t, filepath.Join(root, "admin-bridge"), "admin", "admin.html")

	tests := []struct {
		name      string
		adminFlag []string
		wantAdmin bool
	}{
		{name: "explicit admin bridge", adminFlag: []string{"--admin", adminBridge}, wantAdmin: true},
		{name: "no admin", adminFlag: nil, wantAdmin: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			cmd := newRootCommand()
			var stdout, stderr strings.Builder
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			args := []string{"generate", "--domain", domainPath, "--bridge", mainBridge, "--output", out, "--non-interactive"}
			args = append(args, tc.adminFlag...)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("generate: %v\nstderr: %s", err, stderr.String())
			}

			_, mainErr := os.Stat(filepath.Join(out, "main.txt"))
			if mainErr != nil {
				t.Errorf("main output missing: %v", mainErr)
			}
			_, adminErr := os.Stat(filepath.Join(out, "admin.html"))
			if tc.wantAdmin {
				if adminErr != nil {
					t.Errorf("admin output missing: %v", adminErr)
				}
				if !strings.Contains(stderr.String(), "admin file(s)") {
					t.Errorf("diagnostics do not report admin files: %q", stderr.String())
				}
			} else if adminErr == nil {
				t.Error("admin output was generated without --admin")
			}
		})
	}
}

// seedAdminAlpineCache puts a minimal admin-alpine bridge in the isolated bridge
// cache so bare `--admin` resolves offline to a predictable fixture. The metadata
// marks the cache as freshly checked, which keeps the update probe off the network.
func seedAdminAlpineCache(t *testing.T, template, target string) {
	t.Helper()
	entry := bridge.Default().ByID("admin-alpine")
	if entry == nil {
		t.Fatal("admin-alpine is missing from the bridge registry")
	}
	writeAdminTestBridge(t, bridge.CachePath(*entry), template, target)
	now := time.Now()
	if err := bridge.WriteMeta(*entry, &bridge.CacheMeta{
		ID:            entry.ID,
		Source:        entry.GitHub,
		Version:       "1.0.0",
		ClonedAt:      now,
		LastCheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateAdminFlagForms covers the remaining --admin spellings: an equals
// sign, a bare flag (which defaults to admin-alpine) and an omitted flag. The
// positional `--admin <id>` form is covered by
// TestRootCommandSuppressesCobraErrorNoise in commands_hardening_test.go.
func TestGenerateAdminFlagForms(t *testing.T) {
	appdir.SetBaseForTesting(t.TempDir())
	t.Cleanup(func() { appdir.SetBaseForTesting("") })

	root := t.TempDir()
	domainPath := filepath.Join(root, "domain.yaml")
	domain := "project:\n  name: Fixture\ndatabase: postgresql\nentities:\n  Item:\n    fields:\n      id: uuid [primary]\n"
	if err := os.WriteFile(domainPath, []byte(domain), 0o644); err != nil {
		t.Fatal(err)
	}
	mainBridge := writeAdminTestBridge(t, filepath.Join(root, "main-bridge"), "main", "main.txt")
	adminBridge := writeAdminTestBridge(t, filepath.Join(root, "admin-bridge"), "admin", "admin.html")

	tests := []struct {
		name      string
		adminArgs []string
		seedCache bool
		wantAdmin bool
		wantLog   string
	}{
		{
			name:      "equals form with explicit path",
			adminArgs: []string{"--admin=" + adminBridge},
			wantAdmin: true,
			wantLog:   "admin file(s)",
		},
		{
			name:      "bare flag defaults to admin-alpine",
			adminArgs: []string{"--admin"},
			seedCache: true,
			wantAdmin: true,
			wantLog:   "Rendering admin panel via admin-alpine",
		},
		{
			name:      "flag omitted",
			wantAdmin: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.seedCache {
				seedAdminAlpineCache(t, "admin", "admin.html")
			}
			out := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			cmd := newRootCommand()
			var stdout, stderr strings.Builder
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			args := []string{"generate", "--domain", domainPath, "--bridge", mainBridge, "--output", out, "--non-interactive"}
			args = append(args, tc.adminArgs...)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("generate: %v\nstderr: %s", err, stderr.String())
			}

			if _, err := os.Stat(filepath.Join(out, "main.txt")); err != nil {
				t.Errorf("main output missing: %v", err)
			}
			_, adminErr := os.Stat(filepath.Join(out, "admin.html"))
			if tc.wantAdmin && adminErr != nil {
				t.Errorf("admin output missing: %v", adminErr)
			}
			if !tc.wantAdmin && adminErr == nil {
				t.Error("admin output was generated without --admin")
			}
			if tc.wantLog != "" && !strings.Contains(stderr.String(), tc.wantLog) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tc.wantLog)
			}
		})
	}
}
