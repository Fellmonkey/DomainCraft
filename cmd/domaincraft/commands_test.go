package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	cmd := newRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version failed: %v", err)
	}

	if !strings.Contains(stdout.String(), "version") {
		t.Fatalf("expected version on stdout, got %q", stdout.String())
	}
}

func TestValidateCommandSucceeds(t *testing.T) {
	workDir := t.TempDir()
	domainPath := filepath.Join(workDir, "domain.yaml")
	if err := os.WriteFile(domainPath, []byte(`project:
  name: Validate App

database: postgresql
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required]
`), 0o644); err != nil {
		t.Fatalf("write domain: %v", err)
	}

	cmd := newRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"validate", "--domain", domainPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("validate command failed: %v\nstderr: %s", err, stderr.String())
	}
	// The CLI logger writes progress and diagnostics to stderr; stdout stays data-only.
	if !strings.Contains(stderr.String(), "Schema valid") {
		t.Fatalf("expected 'Schema valid' on stderr, got: %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "Schema valid") {
		t.Fatalf("diagnostics must not leak to stdout, got: %s", stdout.String())
	}
}

func TestGenerateCommandWithoutBridgeShowsError(t *testing.T) {
	workDir := t.TempDir()
	domainPath := filepath.Join(workDir, "domain.yaml")
	bridgeDir := filepath.Join(workDir, "bridges", "nonexistent")
	outputDirPath := filepath.Join(workDir, "generated")

	if err := os.WriteFile(domainPath, []byte(`project:
  name: Generate App
  version: 1.0.0

database: postgresql
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required]
`), 0o644); err != nil {
		t.Fatalf("write domain: %v", err)
	}

	cmd := newRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"generate", "--domain", domainPath, "--bridge", bridgeDir, "--output", outputDirPath})

	// executeRoot (not cmd.Execute) is the path that prints the single error
	// to stderr and tags CLI misuse with errUsage (exit code 2 in main).
	err := executeRoot(cmd)
	if err == nil {
		t.Fatal("generate command should fail with nonexistent bridge")
	}
	if !errors.Is(err, errUsage) {
		t.Errorf("error = %v, want usage error (exit code 2)", err)
	}
	if !strings.Contains(stderr.String(), "bridge") {
		t.Errorf("stderr does not name the bridge: %q", stderr.String())
	}
	// Bridge resolution fails before the renderer runs, so no files may land
	// in the requested output directory.
	if entries, readErr := os.ReadDir(outputDirPath); readErr == nil {
		if len(entries) != 0 {
			t.Errorf("output dir has %d entries after a usage error: %v", len(entries), entries)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("read output dir: %v", readErr)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func TestMustExist(t *testing.T) {
	mustExist(t, os.Args[0])
}
