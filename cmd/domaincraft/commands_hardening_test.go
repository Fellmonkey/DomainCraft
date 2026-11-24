package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/snapshot"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
)

func digestHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func snapshotDeleted(rel string) snapshot.DeletedEntity {
	return snapshot.DeletedEntity{Name: "Old", Files: []snapshot.FileRef{{Path: rel, Custom: true}}}
}

func snapshotRename(rel string) snapshot.Rename {
	return snapshot.Rename{OldName: "Old", NewName: "New", Files: []snapshot.FileRef{{Path: rel, Custom: true}}}
}

// stubTerminal forces the prompt gate for the duration of a test.
func stubTerminal(t *testing.T, value bool) {
	t.Helper()
	old := isTerminal
	isTerminal = func() bool { return value }
	t.Cleanup(func() { isTerminal = old })
}

func TestBridgeEnsureOptionsRespectsNonInteractiveOnTTY(t *testing.T) {
	stubTerminal(t, true)

	cases := []struct {
		name           string
		nonInteractive bool
		updateBridges  bool
		wantPrompt     bool
	}{
		{"interactive prompts", false, false, true},
		{"non-interactive does not prompt", true, false, false},
		{"update-bridges consents without prompting", false, true, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			log := logger.New()
			log.SetWriter(&out)
			opts := &cliOptions{nonInteractive: tt.nonInteractive, updateBridges: tt.updateBridges}
			got := bridgeEnsureOptions(opts, log)
			if tt.wantPrompt != (got.ConfirmUpdate != nil) {
				t.Fatalf("ConfirmUpdate set = %v, want %v", got.ConfirmUpdate != nil, tt.wantPrompt)
			}
			if !tt.updateBridges || got.ConfirmUpdate == nil {
				return
			}
			_, err := got.ConfirmUpdate(bridge.RegistryEntry{ID: "csharp-core"}, &bridge.Update{LocalVersion: "1.2.3"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "1.2.3") || strings.Contains(out.String(), "available") {
				t.Errorf("log = %q, must not label the cached version as available", out.String())
			}
		})
	}
}

func TestMigrationCleanupDeferredOnTTYWhenNonInteractive(t *testing.T) {
	stubTerminal(t, true)

	t.Run("deleted entity files are kept", func(t *testing.T) {
		root := t.TempDir()
		rel := "src/Old.cs"
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		opts := &cliOptions{outputDir: root, nonInteractive: true}
		var logOut bytes.Buffer
		log := logger.New()
		log.SetWriter(&logOut)
		var fileOut bytes.Buffer

		applied, err := handleDeletedEntity(opts, snapshotDeleted(rel), map[string]bool{}, log, &fileOut)
		if err != nil || applied {
			t.Fatalf("handleDeletedEntity = %v, %v; want false, nil", applied, err)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("orphaned file was touched: %v", err)
		}
		if !strings.Contains(logOut.String(), "orphaned files kept") {
			t.Errorf("missing non-interactive warning: %q", logOut.String())
		}
	})

	t.Run("renamed entity files are kept", func(t *testing.T) {
		root := t.TempDir()
		rel := "src/OldService.cs"
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		opts := &cliOptions{outputDir: root, nonInteractive: true}
		var logOut bytes.Buffer
		log := logger.New()
		log.SetWriter(&logOut)
		var fileOut bytes.Buffer

		applied, err := handleRename(opts, snapshotRename(rel), nil, map[string]bool{}, log, &fileOut)
		if err != nil || applied {
			t.Fatalf("handleRename = %v, %v; want false, nil", applied, err)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("file was touched: %v", err)
		}
		if !strings.Contains(logOut.String(), "files kept") {
			t.Errorf("missing non-interactive warning: %q", logOut.String())
		}
	})
}

func TestLoadAndValidateSendsDiagnosticsToStderr(t *testing.T) {
	cases := []struct {
		name       string
		domain     string
		wantErr    bool
		wantOutput string
	}{
		{
			name: "warning only",
			domain: `project:
  name: Warn App
database: postgresql
enums:
  Unused: [A, B]
entities:
  User:
    fields:
      id: uuid [primary]
`,
			wantOutput: "enum is declared but never used",
		},
		{
			name: "hard error is returned once",
			domain: `project:
  name: Bad App
database: postgresql
entities:
  User:
    fields:
      id: uuid
      email: string
`,
			wantErr: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "domain.yaml")
			if err := os.WriteFile(path, []byte(tt.domain), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := &cobra.Command{}
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			_, err := loadAndValidate(&cliOptions{domainFile: path}, cmd)
			if tt.wantErr != (err != nil) {
				t.Fatalf("err = %v, want error = %v", err, tt.wantErr)
			}
			if stdout.Len() != 0 {
				t.Errorf("diagnostics leaked to stdout: %q", stdout.String())
			}
			if tt.wantOutput != "" && !strings.Contains(stderr.String(), tt.wantOutput) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantOutput)
			}
			if tt.wantErr {
				// The error is aggregated and must not also be printed here.
				if strings.Contains(stderr.String(), "validation failed") {
					t.Errorf("hard errors were printed instead of returned: %q", stderr.String())
				}
				if strings.Count(err.Error(), "User") == 0 {
					t.Errorf("aggregated error lost its details: %q", err.Error())
				}
			}
		})
	}
}

func TestCommandLoggerWritesToStderr(t *testing.T) {
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	commandLogger(cmd).Info("progress")
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "progress") {
		t.Errorf("stderr = %q, want progress", stderr.String())
	}
}

func TestRootCommandSuppressesCobraErrorNoise(t *testing.T) {
	root := newRootCommand()
	if !root.SilenceUsage || !root.SilenceErrors {
		t.Fatalf("SilenceUsage=%v SilenceErrors=%v, want both true", root.SilenceUsage, root.SilenceErrors)
	}

	cases := []struct {
		name    string
		command string // empty = root
		args    []string
		wantErr bool
	}{
		{"root rejects positional argument", "", []string{"extra"}, true},
		{"validate rejects positional argument", "validate", []string{"extra"}, true},
		{"bridges rejects positional argument", "bridges", []string{"extra"}, true},
		{"update rejects positional argument", "update", []string{"extra"}, true},
		{"init rejects positional argument", "init", []string{"extra"}, true},
		{"generate allows one admin argument", "generate", []string{"admin-alpine"}, false},
		{"generate rejects two arguments", "generate", []string{"a", "b"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cmd := root
			if tt.command != "" {
				var found *cobra.Command
				for _, sub := range root.Commands() {
					if sub.Name() == tt.command {
						found = sub
						break
					}
				}
				if found == nil {
					t.Fatalf("subcommand %q not found", tt.command)
				}
				cmd = found
			}
			err := cmd.Args(cmd, tt.args)
			if tt.wantErr != (err != nil) {
				t.Fatalf("Args(%v) = %v, want error = %v", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestCommandTreesHaveIndependentFlags(t *testing.T) {
	validDomain := func(name string) string {
		return `project:
  name: ` + name + `
database: postgresql
entities:
  User:
    fields:
      id: uuid [primary]
`
	}
	path := filepath.Join(t.TempDir(), "tree-a.yaml")
	if err := os.WriteFile(path, []byte(validDomain("Tree A")), 0o644); err != nil {
		t.Fatal(err)
	}

	first := newRootCommand()
	first.SetOut(io.Discard)
	first.SetErr(io.Discard)
	first.SetArgs([]string{"validate", "--domain", path})
	if err := first.Execute(); err != nil {
		t.Fatalf("first tree: %v", err)
	}

	second := newRootCommand()
	if got := second.PersistentFlags().Lookup("domain").Value.String(); got != defaultDomainFile {
		t.Errorf("second tree domain flag = %q, want %q", got, defaultDomainFile)
	}
	if got := second.PersistentFlags().Lookup("output").Value.String(); got != defaultOutputDir {
		t.Errorf("second tree output flag = %q, want %q", got, defaultOutputDir)
	}
}

func TestExecutePrintsErrorOnce(t *testing.T) {
	oldArgs, oldStderr := os.Args, os.Stderr
	os.Args = []string{"domaincraft", "validate", "--domain", filepath.Join(t.TempDir(), "missing.yaml")}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Args, os.Stderr = oldArgs, oldStderr
		_ = w.Close()
		_ = r.Close()
	})

	if err := Execute(); err == nil {
		t.Fatal("expected Execute to return the validation error")
	}
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if n := strings.Count(got, "read domain file"); n != 1 {
		t.Errorf("error printed %d times, want 1: %q", n, got)
	}
	if strings.Contains(got, "Usage:") {
		t.Errorf("usage was printed despite SilenceUsage: %q", got)
	}
}
