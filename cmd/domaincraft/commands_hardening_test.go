package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
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
		cmd := &cobra.Command{}
		var fileOut bytes.Buffer
		cmd.SetOut(&fileOut)

		diff := &snapshot.Diff{Deleted: []snapshot.DeletedEntity{snapshotDeleted(rel)}}
		applied, err := applyMigrationActions(opts, diff, map[string]bool{}, log, cmd)
		if err != nil || applied {
			t.Fatalf("applyMigrationActions = %v, %v; want false, nil", applied, err)
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
		applied, err := handleRename(&mc, snapshotRename(rel), nil)
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
		opts := &cliOptions{outputDir: output, prune: true}
		ren := snapshot.Rename{
			OldName: "Old",
			NewName: "New",
			Files:   []snapshot.FileRef{{Path: oldRel, Custom: true}},
		}
		diff := &snapshot.Diff{Renamed: []snapshot.Rename{ren}}
		// The scaffolded destination is marked as freshly written, which
		// triggers the replace path in the rename handler.
		fresh := map[string]bool{newRel: true}
		cmd := &cobra.Command{}
		cmd.SetOut(&bytes.Buffer{})
		applied, err := applyMigrationActions(opts, diff, fresh, logger.New(), cmd)
		if applied || err == nil {
			t.Fatalf("applyMigrationActions = %v, %v; want a hard error under --prune", applied, err)
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
	reportCommandError(w, err)
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
	oldArgs, oldVersion, oldCtx, oldNewRoot := os.Args, version, bridge.CommandContext(), newRootCommandFn
	version = "dev"
	// run() cancels the context it installs via its deferred stop(); restore
	// the shared command context so later tests keep a live one.
	t.Cleanup(func() {
		os.Args, version, newRootCommandFn = oldArgs, oldVersion, oldNewRoot
		bridge.SetCommandContext(oldCtx)
	})

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

	initPath := filepath.Join(t.TempDir(), "domain.yaml")
	if err := os.WriteFile(initPath, []byte("project:\n  name: Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bridgeDir := pruneBridge(t, "")
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"init missing scenario", []string{"domaincraft", "init", "--non-interactive", "--domain", filepath.Join(t.TempDir(), "missing.yaml")}, 2},
		{"init unknown scenario", []string{"domaincraft", "init", "--scenario", "nope", "--domain", filepath.Join(t.TempDir(), "other.yaml")}, 2},
		{"init existing file", []string{"domaincraft", "init", "--scenario", "shop", "--domain", initPath}, 2},
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
			os.Args = tc.args
			newRootCommandFn = func() *cobra.Command {
				root := newRootCommand()
				root.SetErr(f)
				return root
			}
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
	if n := strings.Count(stderr.String(), "echo SAFE"); n != 1 {
		t.Fatalf("command was displayed %d times in stderr: stdout=%q stderr=%q", n, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("migration diagnostics leaked into stdout: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("raw escape byte reached stderr: %q", stderr.String())
	}
	for _, injected := range []string{"\x1b[31m", "\x1b]0;", "\x07"} {
		if strings.Contains(stderr.String(), injected) {
			t.Errorf("raw escape byte %q reached stderr: %q", injected, stderr.String())
		}
	}
}

func TestSanitizeStreamChunk(t *testing.T) {
	in := "line \x1b[31mRED\x1b[0m\n\u202ehidden\u2066\tend"
	got := sanitizeStreamChunk(in)
	if strings.Contains(got, "\x1b") {
		t.Errorf("ANSI escape reached output: %q", got)
	}
	if !strings.Contains(got, "\n") || !strings.Contains(got, "\t") {
		t.Errorf("line breaks or tabs were lost: %q", got)
	}
	if !strings.Contains(got, "<U+202E>") {
		t.Errorf("bidi control was not made visible: %q", got)
	}
}

// stubMigrationExec swaps the child-process constructor for one test. Tests in
// this package are not parallel, so swapping the seam is race-free.
func stubMigrationExec(t *testing.T, fn func(context.Context, string, ...string) *exec.Cmd) {
	t.Helper()
	old := execCommandFn
	execCommandFn = fn
	t.Cleanup(func() { execCommandFn = old })
}

// migrationTestCmd builds a shell command for a stub. It mirrors the shell
// selection in runDatabaseMigrations so a stub can choose its own command line
// while still receiving the context from production.
func migrationTestCmd(ctx context.Context, line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", line)
	}
	return exec.CommandContext(ctx, "sh", "-c", line)
}

// TestRunDatabaseMigrationsPropagatesFailure covers the execution branch: a
// non-zero exit code aborts the run and names the failing command.
func TestRunDatabaseMigrationsPropagatesFailure(t *testing.T) {
	stubMigrationExec(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return migrationTestCmd(ctx, "exit 7")
	})
	cfg := &renderer.MigrationConfig{Enabled: true, Commands: []string{"echo harmless"}}
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	log := logger.New()
	log.SetWriter(&stderr)

	err := runDatabaseMigrations(cfg, t.TempDir(), cmd, log)
	if err == nil {
		t.Fatal("runDatabaseMigrations succeeded with a failing command")
	}
	if !strings.Contains(err.Error(), "database migration 1 failed") {
		t.Errorf("err = %q, want it to name migration 1", err)
	}
	if !strings.Contains(err.Error(), "exit status 7") {
		t.Errorf("err = %q, want the child's exit status", err)
	}
	if strings.Contains(stderr.String(), "Database migrations applied") {
		t.Errorf("success reported after a failed command: %q", stderr.String())
	}
}

// TestRunDatabaseMigrationsUsesOutputDir checks that child processes inherit
// the generated output directory as their working directory.
func TestRunDatabaseMigrationsUsesOutputDir(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "generated")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var captured *exec.Cmd
	stubMigrationExec(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		captured = migrationTestCmd(ctx, "exit 0")
		return captured
	})
	cfg := &renderer.MigrationConfig{Enabled: true, Commands: []string{"echo harmless"}}
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	log := logger.New()
	log.SetWriter(&stderr)

	if err := runDatabaseMigrations(cfg, outDir, cmd, log); err != nil {
		t.Fatalf("runDatabaseMigrations: %v", err)
	}
	if captured == nil {
		t.Fatal("no migration command was started")
	}
	if captured.Dir != outDir {
		t.Errorf("command Dir = %q, want %q", captured.Dir, outDir)
	}
	// Child output is buffered for sanitization and then written to cmd stderr.
	if captured.Stdout == nil || captured.Stderr == nil || captured.Stdout != captured.Stderr {
		t.Errorf("child streams = (%v, %v), want one shared sanitizing buffer", captured.Stdout, captured.Stderr)
	}
	if stdout.Len() != 0 {
		t.Errorf("migration diagnostics leaked into stdout: %q", stdout.String())
	}
	// The stubbed child produces no output, so only the displayed command remains.
	if n := strings.Count(stderr.String(), "echo harmless"); n != 1 {
		t.Errorf("command text appeared %d times in stderr, want 1: %q", n, stderr.String())
	}
}

// TestRunDatabaseMigrationsRespectsCommandContext checks that the shared
// command context is handed to the child and that a canceled context prevents
// the process from starting at all.
func TestRunDatabaseMigrationsRespectsCommandContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	oldCtx := bridge.CommandContext()
	bridge.SetCommandContext(ctx)
	t.Cleanup(func() { bridge.SetCommandContext(oldCtx) })

	var seenCtx context.Context
	var captured *exec.Cmd
	stubMigrationExec(t, func(c context.Context, _ string, _ ...string) *exec.Cmd {
		seenCtx = c
		captured = migrationTestCmd(c, "echo SHOULD_NOT_RUN")
		return captured
	})
	cfg := &renderer.MigrationConfig{Enabled: true, Commands: []string{"echo harmless"}}
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	log := logger.New()
	log.SetWriter(&stderr)

	err := runDatabaseMigrations(cfg, t.TempDir(), cmd, log)
	if err == nil {
		t.Fatal("runDatabaseMigrations succeeded with a canceled context")
	}
	if seenCtx != ctx {
		t.Errorf("command received context %v, want the canceled command context", seenCtx)
	}
	if captured == nil || captured.Process != nil {
		t.Fatalf("child process was started with a canceled context: %+v", captured)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "database migration 1 failed") {
		t.Errorf("err = %q, want it to name migration 1", err)
	}
}

// TestRunDatabaseMigrationsRunsCommandsInOrder appends from two real shell
// commands into one file, so the file records execution order rather than the
// order in which commands were merely displayed.
func TestRunDatabaseMigrationsRunsCommandsInOrder(t *testing.T) {
	outDir := t.TempDir()
	cfg := &renderer.MigrationConfig{
		Enabled:  true,
		Commands: []string{"echo FIRST >> order.txt", "echo SECOND >> order.txt"},
	}
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	log := logger.New()
	log.SetWriter(&stderr)

	if err := runDatabaseMigrations(cfg, outDir, cmd, log); err != nil {
		t.Fatalf("runDatabaseMigrations: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "order.txt"))
	if err != nil {
		t.Fatalf("read migration output: %v", err)
	}
	got := strings.Fields(string(data))
	if len(got) != 2 || got[0] != "FIRST" || got[1] != "SECOND" {
		t.Errorf("execution order = %q, want [FIRST SECOND]", got)
	}
	lines := stderr.String()
	first, second := strings.Index(lines, "Migration 1/2"), strings.Index(lines, "Migration 2/2")
	if first < 0 || second < 0 || first > second {
		t.Errorf("progress log order = %q, want 1/2 before 2/2", lines)
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
		admin   bool // --admin flag is present, so one positional arg is its value
		wantErr bool
	}{
		{"root rejects positional argument", "", []string{"extra"}, false, true},
		{"validate rejects positional argument", "validate", []string{"extra"}, false, true},
		{"bridges rejects positional argument", "bridges", []string{"extra"}, false, true},
		{"update rejects positional argument", "update", []string{"extra"}, false, true},
		{"init rejects positional argument", "init", []string{"extra"}, false, true},
		{"generate rejects bare positional argument", "generate", []string{"admin-alpine"}, false, true},
		{"generate allows one admin argument", "generate", []string{"admin-alpine"}, true, false},
		{"generate rejects two arguments", "generate", []string{"a", "b"}, false, true},
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
			if tt.admin {
				if err := cmd.Flags().Set("admin", "admin-alpine"); err != nil {
					t.Fatal(err)
				}
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
	oldArgs, oldNewRoot := os.Args, newRootCommandFn
	os.Args = []string{"domaincraft", "validate", "--domain", filepath.Join(t.TempDir(), "missing.yaml")}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	newRootCommandFn = func() *cobra.Command {
		root := newRootCommand()
		root.SetErr(w)
		return root
	}
	t.Cleanup(func() {
		os.Args, newRootCommandFn = oldArgs, oldNewRoot
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
