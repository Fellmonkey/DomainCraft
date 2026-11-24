package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/DomainCraft/DomainCraft/internal/interactive"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/internal/snapshot"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

// TestApplyMigrationActionsRewritesFieldRenames covers the --prune branch that
// rewrites identifiers in developer-owned files. Progress goes through the
// logger (stderr); stdout stays reserved for command data.
func TestApplyMigrationActionsRewritesFieldRenames(t *testing.T) {
	root := t.TempDir()
	opts := &cliOptions{outputDir: root, prune: true}

	custom := filepath.Join(root, "src", "ItemService.cs")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("class ItemService { public string Title; var title = 1; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := &snapshot.Diff{FieldRenames: []snapshot.FieldRename{{
		Entity:      "Item",
		OldField:    "title",
		NewField:    "name",
		CustomFiles: []string{"src/ItemService.cs"},
	}}}

	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	log := logger.New()
	log.SetWriter(&stderr)

	applied, err := applyMigrationActions(opts, diff, map[string]bool{}, log, cmd)
	if err != nil || !applied {
		t.Fatalf("applyMigrationActions = %v, %v; want true, nil", applied, err)
	}
	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "class ItemService { public string Name; var name = 1; }" {
		t.Fatalf("custom file not rewritten: %q", data)
	}
	if !strings.Contains(stderr.String(), "updated field title -> name") {
		t.Fatalf("progress missing from stderr: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("progress leaked to stdout: %q", stdout.String())
	}
}

func TestBridgeCommandsAllowed(t *testing.T) {
	tests := []struct {
		name      string
		community bool
		allow     bool
		want      bool
	}{
		{name: "official bridge", community: false, allow: false, want: true},
		{name: "community bridge without consent", community: true, allow: false, want: false},
		{name: "community bridge with consent", community: true, allow: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bridgeCommandsAllowed(tt.community, tt.allow); got != tt.want {
				t.Fatalf("bridgeCommandsAllowed(%v, %v) = %v, want %v", tt.community, tt.allow, got, tt.want)
			}
		})
	}
}

func TestChooseMigrationFiles(t *testing.T) {
	files := []snapshot.FileRef{{Path: "src/A.cs"}, {Path: "src/B.cs"}}

	t.Run("prune selects every file without prompting", func(t *testing.T) {
		opts := &cliOptions{prune: true}

		selected, err := chooseMigrationFiles(opts, files, func([]interactive.FileChoice) ([]string, error) {
			t.Fatal("prompt must not be called in --prune mode")
			return nil, nil
		})
		if err != nil || len(selected) != 2 || !selected["src/A.cs"] || !selected["src/B.cs"] {
			t.Fatalf("selected = %v, %v", selected, err)
		}
	})

	t.Run("prompt result is respected", func(t *testing.T) {
		opts := &cliOptions{}

		selected, err := chooseMigrationFiles(opts, files, func(choices []interactive.FileChoice) ([]string, error) {
			if len(choices) != 2 {
				t.Fatalf("choices = %d, want 2", len(choices))
			}
			return []string{"src/B.cs"}, nil
		})
		if err != nil || len(selected) != 1 || !selected["src/B.cs"] {
			t.Fatalf("selected = %v, %v", selected, err)
		}
	})
}

// --- hermetic CLI fixtures for --prune orchestration ---

// pruneBridge writes a minimal bridge with one regenerated and one scaffolded
// (overwrite: false) template. The optional migrations block is appended as-is.
func pruneBridge(t *testing.T, migrations string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bridge.yaml", `name: fixture-bridge
version: "1.0.0"
output_dir: generated
templates:
  - for: entity
    source: templates/generated.tmpl
    target: "src/{{ .Entity.Name }}.g.txt"
  - for: entity
    source: templates/custom.tmpl
    target: "src/{{ .Entity.Name }}Service.txt"
    overwrite: false
`+migrations)
	write("templates/generated.tmpl", "generated {{ .Entity.Name }}\n")
	write("templates/custom.tmpl", "class {{ .Entity.Name }}Service { {{ .Entity.Name }} id; }\n")
	return dir
}

func pruneDomainText(entity string) string {
	return "project:\n  name: Fixture\ndatabase: postgresql\nentities:\n" + entity
}

func pruneEntity(name string) string {
	return "  " + name + ":\n    fields:\n      id: uuid [primary]\n"
}

func writeDomainFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "domain.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runPruneCLI(t *testing.T, domainPath, bridgeDir, output string, extra ...string) (string, string, error) {
	t.Helper()
	oldVersion := version
	version = "dev"
	t.Cleanup(func() { version = oldVersion })

	cmd := newRootCommand()
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	args := []string{"generate", "--domain", domainPath, "--bridge", bridgeDir, "--output", output, "--non-interactive"}
	args = append(args, extra...)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func writePruneSnapshot(t *testing.T, output string, snap *snapshot.Snapshot) {
	t.Helper()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(output, snapshot.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot.Path(output), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readPruneSnapshot(t *testing.T, output string) *snapshot.Snapshot {
	t.Helper()
	snap, err := snapshot.Load(snapshot.Path(output))
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
		t.Fatal("no snapshot on disk")
	}
	return snap
}

func requireFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should not exist (stat err = %v)", path, err)
	}
}

// TestPruneCLIOrphanCleanup drives the whole generate pipeline with a seeded
// snapshot: deleted orphan, renamed custom file and freshly regenerated file.
func TestPruneCLIOrphanCleanup(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, bridge, output string) string
		check   func(t *testing.T, output string)
	}{
		{
			name: "deleted orphan file removed",
			prepare: func(t *testing.T, bridge, output string) string {
				generateFixture(t, pruneDomainText(pruneEntity("Old")), bridge, output)
				return writeDomainFile(t, pruneDomainText(pruneEntity("Keep")))
			},
			check: func(t *testing.T, output string) {
				requireAbsent(t, filepath.Join(output, "src", "Old.g.txt"))
				requireAbsent(t, filepath.Join(output, "src", "OldService.txt"))
				requireFile(t, filepath.Join(output, "src", "Keep.g.txt"))
				snap := readPruneSnapshot(t, output)
				if _, ok := snap.Entities["Old"]; ok {
					t.Error("deleted entity still in snapshot")
				}
				if _, ok := snap.Entities["Keep"]; !ok {
					t.Error("new entity missing from snapshot")
				}
			},
		},
		{
			name: "renamed custom file renamed with content rewritten",
			prepare: func(t *testing.T, bridge, output string) string {
				generateFixture(t, pruneDomainText(pruneEntity("Old")), bridge, output)
				custom := filepath.Join(output, "src", "OldService.txt")
				if err := os.WriteFile(custom, []byte("class OldService { Old id; }\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				next := "  New:\n    old_name: Old\n    fields:\n      id: uuid [primary]\n"
				return writeDomainFile(t, pruneDomainText(next))
			},
			check: func(t *testing.T, output string) {
				requireAbsent(t, filepath.Join(output, "src", "OldService.txt"))
				requireAbsent(t, filepath.Join(output, "src", "Old.g.txt"))
				content := requireFile(t, filepath.Join(output, "src", "NewService.txt"))
				if !strings.Contains(content, "class NewService") || !strings.Contains(content, "New id") {
					t.Fatalf("renamed custom file kept stale identifiers: %q", content)
				}
				snap := readPruneSnapshot(t, output)
				state, ok := snap.Entities["New"]
				if !ok || state.OldName != "Old" {
					t.Fatalf("snapshot rename state = %+v, %v", state, ok)
				}
			},
		},
		{
			name: "regenerated file is never deleted",
			prepare: func(t *testing.T, bridge, output string) string {
				generateFixture(t, pruneDomainText(pruneEntity("Keep")), bridge, output)
				// Point a deleted entity's snapshot at a file this run rewrites.
				snap := readPruneSnapshot(t, output)
				snap.Entities["Old"] = snapshot.EntityState{Fields: map[string]string{"id": "uuid"}}
				snap.Files = append(snap.Files, renderer.RenderedFile{Path: "src/Keep.g.txt", Entity: "Old"})
				writePruneSnapshot(t, output, snap)
				if err := os.WriteFile(filepath.Join(output, "src", "Keep.g.txt"), []byte("stale"), 0o644); err != nil {
					t.Fatal(err)
				}
				return writeDomainFile(t, pruneDomainText(pruneEntity("Keep")))
			},
			check: func(t *testing.T, output string) {
				if got := requireFile(t, filepath.Join(output, "src", "Keep.g.txt")); got != "generated Keep\n" {
					t.Fatalf("fresh file was removed or not regenerated: %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge := pruneBridge(t, "")
			output := t.TempDir()
			domainPath := tt.prepare(t, bridge, output)
			stdout, stderr, err := runPruneCLI(t, domainPath, bridge, output, "--prune")
			if err != nil {
				t.Fatalf("generate --prune failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if strings.Contains(stdout, "  ▸") {
				t.Errorf("progress leaked to stdout: %q", stdout)
			}
			tt.check(t, output)
		})
	}
}

// TestPruneCLICleanupFailureKeepsSnapshot asserts the F01 contract at the CLI
// level: a failed rewrite or delete under --prune returns an error and leaves
// the previous snapshot untouched, so a later run can retry the cleanup.
func TestPruneCLICleanupFailureKeepsSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		entity   string
		fields   map[string]string
		file     renderer.RenderedFile
		wantDiag string
	}{
		{
			name:     "failed field rewrite",
			domain:   "  Item:\n    fields:\n      id: uuid [primary]\n      name: string [old_name:title]\n",
			entity:   "Item",
			fields:   map[string]string{"title": "string"},
			file:     renderer.RenderedFile{Path: "src/LegacyService.txt", Entity: "Item", Custom: true},
			wantDiag: "could not rewrite field references",
		},
		{
			name:     "failed orphan delete",
			domain:   pruneEntity("Keep"),
			entity:   "Old",
			fields:   map[string]string{"id": "uuid"},
			file:     renderer.RenderedFile{Path: "src/OldService.txt", Entity: "Old", Custom: true},
			wantDiag: "could not delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge := pruneBridge(t, "")
			output := t.TempDir()
			writePruneSnapshot(t, output, &snapshot.Snapshot{
				FormatVersion:    snapshot.FormatVersion,
				Bridge:           "SENTINEL",
				ProjectNamespace: "Fixture",
				Entities:         map[string]snapshot.EntityState{tt.entity: {Fields: tt.fields}},
				Files:            []renderer.RenderedFile{tt.file},
			})
			// A non-empty directory cannot be read as a file or removed with
			// os.Remove: both cleanup paths fail without touching the network.
			blocked := filepath.Join(output, filepath.FromSlash(tt.file.Path))
			if err := os.MkdirAll(blocked, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blocked, "child"), []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}

			domainPath := writeDomainFile(t, pruneDomainText(tt.domain))
			stdout, stderr, err := runPruneCLI(t, domainPath, bridge, output, "--prune")
			if err == nil {
				t.Fatalf("generate --prune should fail; stdout: %s", stdout)
			}
			if !strings.Contains(stderr, tt.wantDiag) {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantDiag)
			}
			if got := readPruneSnapshot(t, output).Bridge; got != "SENTINEL" {
				t.Errorf("snapshot was advanced past failed cleanup: bridge = %q", got)
			}
		})
	}
}

// TestPruneCLICommunityConsentGate checks the gate decision without running
// any bridge-declared shell command.
func TestPruneCLICommunityConsentGate(t *testing.T) {
	const migrations = `migrations:
  enabled: true
  commands:
    - "echo blocked > gate.txt"
`
	marker := func(output string) string { return filepath.Join(output, "gate.txt") }

	t.Run("prune never auto-runs community migrations", func(t *testing.T) {
		bridge := pruneBridge(t, migrations)
		output := t.TempDir()
		generateFixture(t, pruneDomainText(pruneEntity("Old")), bridge, output)
		domainPath := writeDomainFile(t, pruneDomainText(pruneEntity("Keep")))
		stdout, stderr, err := runPruneCLI(t, domainPath, bridge, output, "--prune")
		if err != nil {
			t.Fatalf("generate --prune: %v\n%s", err, stderr)
		}
		if !strings.Contains(stderr, "community bridge(s)") {
			t.Errorf("missing gate warning: %q", stderr)
		}
		if strings.Contains(stdout, "Applying 1 database migration command(s)") {
			t.Errorf("community migration was reviewed for execution: %q", stdout)
		}
		requireAbsent(t, marker(output))
	})

	t.Run("migrate without consent is rejected", func(t *testing.T) {
		bridge := pruneBridge(t, migrations)
		output := t.TempDir()
		domainPath := writeDomainFile(t, pruneDomainText(pruneEntity("Keep")))
		stdout, _, err := runPruneCLI(t, domainPath, bridge, output, "--migrate")
		if err == nil || !strings.Contains(err.Error(), "--allow-bridge-commands") {
			t.Fatalf("err = %v, want consent error", err)
		}
		if strings.Contains(stdout, "Applying 1 database migration command(s)") {
			t.Errorf("migration command was reviewed without consent: %q", stdout)
		}
		requireAbsent(t, marker(output))
	})
}

// TestPruneCLIReportStreams asserts F18: diagnostics (warnings, progress) go to
// stderr through the logger; the multi-line refactoring reports go to stdout.
func TestPruneCLIReportStreams(t *testing.T) {
	bridge := pruneBridge(t, "")
	output := t.TempDir()
	custom := filepath.Join(output, "src", "ItemService.txt")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("class ItemService { string title; string note; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePruneSnapshot(t, output, &snapshot.Snapshot{
		FormatVersion:    snapshot.FormatVersion,
		Bridge:           "fixture-bridge",
		ProjectNamespace: "Fixture",
		Entities: map[string]snapshot.EntityState{
			"Item": {Fields: map[string]string{"title": "string", "note": "string"}},
		},
		Files: []renderer.RenderedFile{{Path: "src/ItemService.txt", Entity: "Item", Custom: true}},
	})
	domain := "  Item:\n    fields:\n      id: uuid [primary]\n      name: string [old_name:title]\n      note: int\n"
	domainPath := writeDomainFile(t, pruneDomainText(domain))

	stdout, stderr, err := runPruneCLI(t, domainPath, bridge, output, "--prune")
	if err != nil {
		t.Fatalf("generate --prune: %v\n%s", err, stderr)
	}
	for _, want := range []string{"You changed data types in domain.yaml", "You renamed fields in domain.yaml"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing from stdout: %q; stdout = %q", want, stdout)
		}
	}
	if !strings.Contains(stderr, "ACTION REQUIRED") || !strings.Contains(stderr, "updated field title -> name") {
		t.Errorf("warnings/progress missing from stderr: %q", stderr)
	}
	if strings.Contains(stdout, "updated field title -> name") {
		t.Errorf("progress leaked to stdout: %q", stdout)
	}
}
