package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
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

		mc := migrationContext{opts: opts, fresh: map[string]bool{}, log: log, out: &fileOut}
		applied, err := handleDeletedEntity(mc, snapshotDeleted(rel))
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

		mc := migrationContext{opts: opts, fresh: map[string]bool{}, log: log, out: &fileOut}
		applied, err := handleRename(mc, snapshotRename(rel), nil)
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

func TestCleanupDeferred(t *testing.T) {
	stubTerminal(t, true)
	cases := []struct {
		name string
		opts cliOptions
		want bool
	}{
		{"prune on a TTY", cliOptions{prune: true}, false},
		{"prune non-interactive", cliOptions{prune: true, nonInteractive: true}, false},
		{"interactive on a TTY", cliOptions{}, false},
		{"non-interactive on a TTY", cliOptions{nonInteractive: true}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanupDeferred(&tt.opts); got != tt.want {
				t.Fatalf("cleanupDeferred(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
	}

	stubTerminal(t, false)
	if !cleanupDeferred(&cliOptions{}) {
		t.Fatal("cleanup must be deferred without a TTY")
	}
}

// TestPruneRejectsScaffoldReplaceEscape guards the last unvalidated removal in
// --prune: a poisoned rename destination must never delete a file outside the
// output directory, neither via ".." nor through a symlink.
func TestPruneRejectsScaffoldReplaceEscape(t *testing.T) {
	run := func(t *testing.T, output, oldRel, newRel string) {
		t.Helper()
		mc := migrationContext{
			opts:  &cliOptions{outputDir: output, prune: true},
			fresh: map[string]bool{newRel: true}, // scaffolded destination triggers the replace path
			log:   logger.New(),
			out:   &bytes.Buffer{},
		}
		ren := snapshot.Rename{
			OldName: "Old",
			NewName: "New",
			Files:   []snapshot.FileRef{{Path: oldRel, Custom: true}},
		}
		applied, err := handleRename(mc, ren, &snapshot.Diff{})
		if applied || err == nil {
			t.Fatalf("handleRename = %v, %v; want a hard error under --prune", applied, err)
		}
		if !strings.Contains(err.Error(), "replace scaffolded") {
			t.Errorf("err = %v, want it to name the scaffold replacement", err)
		}
	}

	t.Run("path traversal", func(t *testing.T) {
		root := t.TempDir()
		output := filepath.Join(root, "generated")
		outside := filepath.Join(root, "outside")
		for _, dir := range []string{output, outside} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		victim := filepath.Join(outside, "NewService.txt")
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, output, "../outside/OldService.txt", "../outside/NewService.txt")
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("file outside outputDir was removed: %v", err)
		}
	})

	t.Run("symlink to outside directory", func(t *testing.T) {
		root := t.TempDir()
		output := filepath.Join(root, "generated")
		outside := filepath.Join(root, "outside")
		for _, dir := range []string{output, outside} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		victim := filepath.Join(outside, "NewService.txt")
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(output, "link")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		run(t, output, "link/OldService.txt", "link/NewService.txt")
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("file behind the symlink was removed: %v", err)
		}
	})
}

// TestPruneCleanupErrorPrintedOnce checks that a failed namespace rewrite under
// --prune is returned (and printed by reportCommandError) but never warned about
// a second time by the cleanup handler itself.
func TestPruneCleanupErrorPrintedOnce(t *testing.T) {
	root := t.TempDir()
	// A non-empty directory at the custom file path makes RewriteFile fail.
	blocked := filepath.Join(root, "src", "ItemService.txt")
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	diff := &snapshot.Diff{NamespaceRename: &snapshot.NamespaceMismatch{
		OldNamespace: "OldNs",
		NewNamespace: "NewNs",
		Files:        []string{"src/ItemService.txt"},
	}}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
		_ = r.Close()
		_ = w.Close()
	})

	log := logger.New()
	log.SetWriter(w)
	cmd := &cobra.Command{}
	cmd.SetErr(w)
	applied, err := applyMigrationActions(&cliOptions{outputDir: root, prune: true}, diff, nil, log, cmd)
	if applied || err == nil {
		t.Fatalf("applyMigrationActions = %v, %v; want a hard error", applied, err)
	}
	reportCommandError(err)
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if n := strings.Count(got, "rewrite namespace in src/ItemService.txt"); n != 1 {
		t.Errorf("cleanup failure printed %d times, want 1: %q", n, got)
	}
	if strings.Contains(got, "could not rewrite namespace") {
		t.Errorf("duplicate warning was emitted: %q", got)
	}
}

// TestUsageErrorsExitTwo separates CLI misuse (exit 2) from runtime failures
// (exit 1) while keeping the error itself printed exactly once.
func TestUsageErrorsExitTwo(t *testing.T) {
	oldArgs, oldStderr, oldVersion, oldCtx := os.Args, os.Stderr, version, commandCtx
	version = "dev"
	// run() cancels the context it installs via its deferred stop(); restore
	// the package-level context so later tests keep a live one.
	t.Cleanup(func() { os.Args, os.Stderr, version, commandCtx = oldArgs, oldStderr, oldVersion, oldCtx })

	domainPath := filepath.Join(t.TempDir(), "domain.yaml")
	if err := os.WriteFile(domainPath, []byte(`project:
  name: Fixture
database: postgresql
entities:
  Item:
    fields:
      id: uuid [primary]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	bridgeDir := pruneBridge(t, "")
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"unknown flag", []string{"domaincraft", "validate", "--bogus"}, 2},
		{"too many arguments", []string{"domaincraft", "validate", "a", "b"}, 2},
		{"unknown command", []string{"domaincraft", "nope"}, 2},
		{"missing bridge", []string{"domaincraft", "generate", "--domain", domainPath, "--output", t.TempDir(), "--non-interactive"}, 2},
		{"malformed --replace", []string{"domaincraft", "generate", "--domain", domainPath, "--bridge", bridgeDir, "--output", t.TempDir(), "--non-interactive", "--replace", "malformed"}, 2},
		{"runtime failure", []string{"domaincraft", "validate", "--domain", filepath.Join(t.TempDir(), "missing.yaml")}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "stderr.log")
			f, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			os.Args, os.Stderr = tc.args, f
			got := run()
			_ = f.Close()
			if got != tc.want {
				data, _ := os.ReadFile(logPath)
				t.Errorf("run(%v) = %d, want %d; stderr: %s", tc.args, got, tc.want, data)
			}
		})
	}
}

func TestSanitizeCommandLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"CSI", "echo \x1b[2JSAFE", "echo SAFE"},
		{"OSC with BEL", "\x1b]0;pwn\x07echo SAFE", "echo SAFE"},
		{"OSC with ST", "echo SAFE\x1b]0;pwn\x1b\\", "echo SAFE"},
		{"C0 and DEL", "echo\tSAFE\r\n\x00\x07\x7f", `echo\tSAFE\n`},
		{"C1", "echo\u0085SAFE\u009f", "echoSAFE"},
		{"bare ESC", "echo SAFE\x1b", "echo SAFE"},
		{"UTF-8 preserved", "echo Привет", "echo Привет"},
		{"embedded newline cannot split the command", "echo SAFE\nrm -rf /", `echo SAFE\nrm -rf /`},
		{"embedded tab cannot fake a new argument", "echo\tSAFE", `echo\tSAFE`},
		{"bidi override", "echo \u202eSAFE\u202c", "echo <U+202E>SAFE<U+202C>"},
		{"bidi isolates", "\u2066evil\u2069SAFE", "<U+2066>evil<U+2069>SAFE"},
		{"zero-width characters", "e\u200bx\u200dy\u200cz", "e<U+200B>x<U+200D>y<U+200C>z"},
		{"zero-width no-break space", "\ufeffSAFE", "<U+FEFF>SAFE"},
		{"RTL mark", "\u200fSAFE", "<U+200F>SAFE"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeCommandLine(tt.in); got != tt.want {
				t.Fatalf("sanitizeCommandLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRunDatabaseMigrationsSanitizesDisplayedCommand uses a shell comment to
// carry the control bytes: the command stays harmless while exercising the
// consent-review output. Only presentation is sanitized; execution is unchanged.
func TestRunDatabaseMigrationsSanitizesDisplayedCommand(t *testing.T) {
	evil := "\x1b[31mRED\x1b[0m \x1b]0;window-title\x07 \x07"
	command := "echo SAFE # " + evil
	if runtime.GOOS == "windows" {
		command = "echo SAFE & rem " + evil
	}
	cfg := &renderer.MigrationConfig{Enabled: true, Commands: []string{command}}
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	log := logger.New()
	log.SetWriter(&stderr)

	if err := runDatabaseMigrations(cfg, t.TempDir(), cmd, log); err != nil {
		t.Fatalf("runDatabaseMigrations: %v", err)
	}
	if !strings.Contains(stdout.String(), "echo SAFE") || !strings.Contains(stderr.String(), "echo SAFE") {
		t.Fatalf("command was not displayed: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b") {
		t.Errorf("raw escape byte reached stdout: %q", stdout.String())
	}
	for _, injected := range []string{"\x1b[31m", "\x1b]0;", "\x07"} {
		if strings.Contains(stderr.String(), injected) {
			t.Errorf("raw escape byte %q reached stderr: %q", injected, stderr.String())
		}
	}
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
