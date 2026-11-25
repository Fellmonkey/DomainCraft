package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
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
