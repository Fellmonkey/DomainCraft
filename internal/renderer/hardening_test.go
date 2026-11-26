package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

// TestBeforeCommitHookConcurrentAccess would report a data race under -race
// if the test seam were an unsynchronized package-level func variable.
func TestBeforeCommitHookConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			setBeforeCommitHook(func() {})
		}()
		go func() {
			defer wg.Done()
			_ = currentBeforeCommitHook()
		}()
	}
	wg.Wait()
	called := false
	setBeforeCommitHook(func() { called = true })
	if hook := currentBeforeCommitHook(); hook == nil {
		t.Fatal("hook was lost after concurrent access")
	} else {
		hook()
	}
	if !called {
		t.Fatal("retrieved hook was not the stored hook")
	}
	setBeforeCommitHook(nil)
}

// writeHardeningBridge lays out a minimal bridge directory for the filesystem
// hardening tests. Keys are forward-slash relative paths.
func writeHardeningBridge(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// R1: a symlink at the final target component must be rejected before the
// rename, and the file it points to must stay untouched.
func TestRenderRejectsSymlinkAtFinalComponent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
templates:
  - for: project
    source: templates/p.tmpl
    target: "leak.txt"
`,
		"templates/p.tmpl": `generated`,
	})
	out := filepath.Join(base, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	link := filepath.Join(out, "leak.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err == nil {
		t.Fatal("Render accepted a symlink at the final target component")
	}

	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Errorf("outside file changed: data=%q err=%v", data, err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("target link was replaced or removed: info=%v err=%v", info, err)
	}
}

// F15: the bridge-source symlink check must use an absolute bridge directory.
// With a relative bridge dir, a symlinked final source component used to be
// compared against a relative base and slipped through.
func TestRenderRejectsSymlinkWithRelativeBridgeDir(t *testing.T) {
	t.Run("symlinked template source", func(t *testing.T) {
		base := t.TempDir()
		// CI on Windows puts the checkout (D:) and the system temp dir (C:) on
		// different volumes; filepath.Rel cannot express a cross-volume path.
		// Chdir into base so the bridge and cwd share a volume. This test does
		// not call t.Parallel, and the testing package keeps parallel tests
		// paused until sequential tests finish.
		t.Chdir(base)
		outside := filepath.Join(base, "outside.tmpl")
		if err := os.WriteFile(outside, []byte("leaked"), 0o644); err != nil {
			t.Fatalf("write outside template: %v", err)
		}
		manifest := func(source string) string {
			return `name: demo
contract_version: 1
templates:
  - for: project
    source: ` + source + `
    target: "out.txt"
`
		}
		bridge := writeHardeningBridge(t, map[string]string{
			"bridge.yaml":         manifest("templates/good.tmpl"),
			"templates/good.tmpl": `good`,
		})
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		relBridge, err := filepath.Rel(cwd, bridge)
		if err != nil {
			t.Fatalf("relative bridge dir: %v", err)
		}
		out := filepath.Join(base, "out")
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatalf("mkdir output: %v", err)
		}
		// Positive control: a relative bridge dir alone must work.
		r, err := New(relBridge, nil)
		if err != nil {
			t.Fatalf("New(%q) error = %v", relBridge, err)
		}
		if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err != nil {
			t.Fatalf("Render with relative bridge dir failed: %v", err)
		}
		if err := os.Remove(filepath.Join(out, "out.txt")); err != nil {
			t.Fatalf("remove control output: %v", err)
		}

		// Same relative dir, but the source's final component is a symlink out.
		if err := os.WriteFile(filepath.Join(bridge, "bridge.yaml"), []byte(manifest("templates/p.tmpl")), 0o644); err != nil {
			t.Fatalf("rewrite bridge.yaml: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(bridge, "templates", "p.tmpl")); err != nil {
			t.Skipf("symlinks unavailable on this host: %v", err)
		}
		r, err = New(relBridge, nil)
		if err != nil {
			t.Fatalf("New(%q) error = %v", relBridge, err)
		}
		if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err == nil {
			t.Fatal("Render accepted a symlinked template source in a relative bridge dir")
		}
		if _, err := os.Stat(filepath.Join(out, "out.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("target was written despite rejected source: %v", err)
		}
	})
}

// R2: a template that parses but fails during Execute must not truncate the
// previous contents of the target, and must leave no temp file behind.
func TestRenderExecuteErrorPreservesPreviousOutput(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
templates:
  - for: project
    source: templates/p.tmpl
    target: "value.txt"
`,
		// Parse succeeds; index out of range fails only at execution time.
		"templates/p.tmpl": `{{ index .Project.Entities 99 }}`,
	})
	out := filepath.Join(tmp, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	target := filepath.Join(out, "value.txt")
	if err := os.WriteFile(target, []byte("valid"), 0o644); err != nil {
		t.Fatalf("write prior output: %v", err)
	}

	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err == nil {
		t.Fatal("Render succeeded despite an execute error")
	}

	data, err := os.ReadFile(target)
	if err != nil || string(data) != "valid" {
		t.Errorf("prior output destroyed: data=%q err=%v", data, err)
	}
	assertNoRenderTemps(t, out)
}

// R3: an error in a later template must not leave earlier files written. The
// second template fails to parse, so no file may reach outputDir at all.
func TestRenderFailureRollsBackPriorWrites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		prior       string // contents of a.txt before the run; empty = absent
		wantContent string // expected contents after failure; empty = absent
	}{
		{name: "new file removed on later error", prior: "", wantContent: ""},
		{name: "existing file restored on later error", prior: "original", wantContent: "original"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			bridge := writeHardeningBridge(t, map[string]string{
				"bridge.yaml": `name: demo
templates:
  - for: project
    source: templates/good.tmpl
    target: "a.txt"
  - for: project
    source: templates/bad.tmpl
    target: "b.txt"
`,
				"templates/good.tmpl": `new-content`,
				"templates/bad.tmpl":  `{{ if }}`, // parse error
			})
			out := filepath.Join(tmp, "out")
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatalf("mkdir output: %v", err)
			}
			a := filepath.Join(out, "a.txt")
			if tt.prior != "" {
				if err := os.WriteFile(a, []byte(tt.prior), 0o644); err != nil {
					t.Fatalf("write prior a.txt: %v", err)
				}
			}

			r, err := New(bridge, nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err == nil {
				t.Fatal("Render succeeded despite a parse error in the second template")
			}

			data, statErr := os.ReadFile(a)
			if tt.wantContent == "" {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("a.txt should be absent after rollback: data=%q err=%v", data, statErr)
				}
			} else if statErr != nil || string(data) != tt.wantContent {
				t.Errorf("a.txt = %q (err=%v), want %q", data, statErr, tt.wantContent)
			}
			if _, err := os.Stat(filepath.Join(out, "b.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("b.txt should not exist after failed render: %v", err)
			}
			assertNoRenderTemps(t, out)
		})
	}
}

// F24: a target that existed at staging but was deleted before commit must be
// rolled back as a new file (removed). Commit leaves no backup in that case,
// so rollback removes the file instead of restoring a nonexistent original.
func TestRenderRollbackRemovesTargetDeletedBeforeCommit(t *testing.T) {
	base := t.TempDir()
	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
contract_version: 1
templates:
  - for: project
    source: templates/good.tmpl
    target: "a.txt"
  - for: project
    source: templates/block.tmpl
    target: "b.txt"
`,
		"templates/good.tmpl":  `new-content`,
		"templates/block.tmpl": `never-committed`,
	})
	out := filepath.Join(base, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	a := filepath.Join(out, "a.txt")
	if err := os.WriteFile(a, []byte("original"), 0o644); err != nil {
		t.Fatalf("write prior a.txt: %v", err)
	}
	// Symlinks are rejected by the Phase 2 check, but only ones that appear
	// after staging reach that check: Phase 1 rejects them too.
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	probe := filepath.Join(base, "probe.link")
	if err := os.Symlink(outside, probe); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	_ = os.Remove(probe)

	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	setBeforeCommitHook(func() {
		if err := os.Remove(a); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove staged target: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(out, "b.txt")); err != nil {
			t.Errorf("plant symlink before commit: %v", err)
		}
	})
	t.Cleanup(func() { setBeforeCommitHook(nil) })

	if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err == nil {
		t.Fatal("Render succeeded despite a blocked commit")
	}
	if _, err := os.Stat(a); !errors.Is(err, os.ErrNotExist) {
		data, readErr := os.ReadFile(a)
		t.Errorf("a.txt survived rollback: statErr=%v data=%q readErr=%v", err, data, readErr)
	}
	assertNoRenderTemps(t, out)
}

// A directory swapped for a symlink after staging must not redirect the commit.
// A plain os.Lstat at commit time sees only the leaf and would miss this; the
// whole write path now goes through os.Root, which refuses to follow the link.
func TestRenderRejectsParentSymlinkPlantedBeforeCommit(t *testing.T) {
	base := t.TempDir()
	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
contract_version: 1
templates:
  - for: project
    source: templates/good.tmpl
    target: "src/App.cs"
`,
		"templates/good.tmpl": `generated`,
	})
	out := filepath.Join(base, "out")
	src := filepath.Join(out, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	probe := filepath.Join(base, "probe.link")
	if err := os.Symlink(outside, probe); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	_ = os.Remove(probe)

	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	setBeforeCommitHook(func() {
		if err := os.RemoveAll(src); err != nil {
			t.Errorf("remove staged src: %v", err)
		}
		if err := os.Symlink(outside, src); err != nil {
			t.Errorf("plant parent symlink: %v", err)
		}
	})
	t.Cleanup(func() { setBeforeCommitHook(nil) })

	if _, _, err := r.Render(&ir.Project{Name: "P"}, out); err == nil {
		t.Fatal("Render succeeded after parent directory was replaced by a symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "App.cs")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rendered file escaped outputDir through a parent symlink")
	}
	assertNoRenderTemps(t, out)
}

func assertNoRenderTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".domaincraft-render-") {
			t.Errorf("leftover render temp file: %s", e.Name())
		}
	}
}

// F17: a failed restore used to be discarded silently while the comment claimed
// no half-written output. Make the restore target's directory unwritable and
// require the failure to surface.
func TestRollbackWritesReportsRestoreFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	backup := filepath.Join(dir, "a.txt.bak")
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(backup, []byte("original"), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	// Some platforms (or a root test process) ignore directory write bits.
	probe := filepath.Join(dir, ".probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err == nil {
		_ = os.Remove(probe)
		t.Skip("directory permissions are not enforced on this host")
	}

	err := rollbackWrites(nil, []stagedWrite{{target: target, backup: backup}})
	if err == nil {
		t.Fatal("rollbackWrites silently discarded a failed restore")
	}
	if !strings.Contains(err.Error(), "restore") {
		t.Errorf("error = %q, want it to name the failed restore", err.Error())
	}
}

// The primary rendering error stays first and both errors remain inspectable
// via errors.Is.
func TestRollbackErrorKeepsOriginalPrimary(t *testing.T) {
	t.Parallel()
	primary := errors.New("commit failed")
	rollback := errors.New("restore failed")
	got := rollbackError(primary, rollback)
	if !errors.Is(got, primary) || !errors.Is(got, rollback) {
		t.Fatalf("errors.Join lost a cause: %v", got)
	}
	if !strings.HasPrefix(got.Error(), primary.Error()) {
		t.Errorf("primary error is not first: %q", got.Error())
	}
	if !errors.Is(rollbackError(primary, nil), primary) {
		t.Error("no-op rollback must return the original error unchanged")
	}
}

func TestCustomTemplateStagedOncePerRun(t *testing.T) {
	t.Parallel()
	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
templates:
  - for: entity
    source: templates/custom.tmpl
    target: "custom.txt"
    overwrite: false
`,
		"templates/custom.tmpl": `entity-{{ .Entity.Name }}`,
	})
	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	project := &ir.Project{Name: "P", Entities: []ir.Entity{
		{Name: "Alpha"},
		{Name: "Beta"},
	}}
	out := filepath.Join(t.TempDir(), "out")
	_, manifest, err := r.Render(project, out)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(manifest) != 2 {
		t.Fatalf("manifest has %d entries, want 2: %v", len(manifest), manifest)
	}
	if !manifest[0].Written || manifest[1].Written {
		t.Fatalf("scaffold-once manifest = %v, want first written and second skipped", manifest)
	}
	data, err := os.ReadFile(filepath.Join(out, "custom.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "entity-Alpha" {
		t.Fatalf("custom file = %q, want entity-Alpha", data)
	}
}

// TestRenderPhasesPreserveManifestAndRollback pins the contract spanning all
// three phases: staging records manifest entries in template/context/target
// order (including skipped scaffolds), and a failure during commit rolls back
// partial writes while returning neither paths nor a manifest.
func TestRenderPhasesPreserveManifestAndRollback(t *testing.T) {
	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
templates:
  - for: entity
    source: templates/entity.tmpl
    target: "{{ .Entity.Name }}.txt"
  - for: project
    source: templates/shared.tmpl
    target: "shared.txt"
  - for: project
    source: templates/custom.tmpl
    target: "custom.txt"
    overwrite: false
`,
		"templates/entity.tmpl": `entity-{{ .Entity.Name }}`,
		"templates/shared.tmpl": `shared`,
		"templates/custom.tmpl": `custom`,
	})
	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	project := &ir.Project{Name: "P", Entities: []ir.Entity{
		{Name: "Alpha", NamePlural: "Alphas"},
		{Name: "Beta", NamePlural: "Betas"},
	}}

	out := filepath.Join(t.TempDir(), "out")
	wantManifest := []RenderedFile{
		{Path: "Alpha.txt", Entity: "Alpha", Written: true},
		{Path: "Beta.txt", Entity: "Beta", Written: true},
		{Path: "shared.txt", Written: true},
		{Path: "custom.txt", Custom: true, Written: true},
	}
	wantWritten := []string{
		filepath.Join(out, "Alpha.txt"),
		filepath.Join(out, "Beta.txt"),
		filepath.Join(out, "shared.txt"),
		filepath.Join(out, "custom.txt"),
	}

	written, manifest, err := r.Render(project, out)
	if err != nil {
		t.Fatalf("Render() 1 error = %v", err)
	}
	if !reflect.DeepEqual(manifest, wantManifest) {
		t.Errorf("manifest = %+v, want %+v", manifest, wantManifest)
	}
	if !reflect.DeepEqual(written, wantWritten) {
		t.Errorf("written = %q, want %q", written, wantWritten)
	}

	// A skipped scaffold keeps its slot in the manifest with Written=false.
	written, manifest, err = r.Render(project, out)
	if err != nil {
		t.Fatalf("Render() 2 error = %v", err)
	}
	wantManifest[3].Written = false
	wantWritten = wantWritten[:3]
	if !reflect.DeepEqual(manifest, wantManifest) {
		t.Errorf("manifest on second run = %+v, want %+v", manifest, wantManifest)
	}
	if !reflect.DeepEqual(written, wantWritten) {
		t.Errorf("written on second run = %q, want %q", written, wantWritten)
	}
	data, err := os.ReadFile(filepath.Join(out, "custom.txt"))
	if err != nil || string(data) != "custom" {
		t.Errorf("developer-owned file changed: data=%q err=%v", data, err)
	}

	// A symlink planted after staging blocks a later commit. The earlier files
	// must be rolled back and the failure must carry no paths or manifest.
	t.Run("commit failure rolls back partial writes", func(t *testing.T) {
		base := t.TempDir()
		outside := filepath.Join(base, "outside.txt")
		if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
			t.Fatalf("write outside file: %v", err)
		}
		probe := filepath.Join(base, "probe.link")
		if err := os.Symlink(outside, probe); err != nil {
			t.Skipf("symlinks unavailable on this host: %v", err)
		}
		_ = os.Remove(probe)

		out2 := filepath.Join(base, "out")
		if err := os.MkdirAll(out2, 0o755); err != nil {
			t.Fatalf("mkdir output: %v", err)
		}
		alpha := filepath.Join(out2, "Alpha.txt")
		if err := os.WriteFile(alpha, []byte("original"), 0o644); err != nil {
			t.Fatalf("write prior Alpha.txt: %v", err)
		}

		setBeforeCommitHook(func() {
			if err := os.Symlink(outside, filepath.Join(out2, "shared.txt")); err != nil {
				t.Errorf("plant symlink before commit: %v", err)
			}
		})
		t.Cleanup(func() { setBeforeCommitHook(nil) })

		written, manifest, err := r.Render(project, out2)
		if err == nil {
			t.Fatal("Render succeeded despite a symlink planted before commit")
		}
		if !strings.Contains(err.Error(), "shared.txt") || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("error = %q, want it to name the blocked target and symlink", err)
		}
		if written != nil || manifest != nil {
			t.Errorf("failed render returned paths=%q manifest=%+v, want both nil", written, manifest)
		}

		data, err := os.ReadFile(alpha)
		if err != nil || string(data) != "original" {
			t.Errorf("backed-up target not restored: data=%q err=%v", data, err)
		}
		if _, err := os.Stat(filepath.Join(out2, "Beta.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("newly committed file survived rollback: %v", err)
		}
		outsideData, err := os.ReadFile(outside)
		if err != nil || string(outsideData) != "keep" {
			t.Errorf("outside file changed: data=%q err=%v", outsideData, err)
		}
		entries, err := os.ReadDir(out2)
		if err != nil {
			t.Fatalf("read output dir: %v", err)
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".domaincraft-render-") || strings.HasSuffix(name, ".bak") {
				t.Errorf("leftover phase artifact: %s", name)
			}
		}
	})
}

// TestRenderCommitPreservesCustomFileCreatedBeforeCommit closes the staging→
// commit race for overwrite:false targets. Staging skipped a custom file that
// already existed, but commit backed up and replaced whatever was there and
// cleanupBackups then deleted the backup, silently losing developer-owned
// bytes. Earlier committed writes must still roll back when the race is hit.
func TestRenderCommitPreservesCustomFileCreatedBeforeCommit(t *testing.T) {
	bridge := writeHardeningBridge(t, map[string]string{
		"bridge.yaml": `name: demo
contract_version: 1
templates:
  - for: project
    source: templates/existing.tmpl
    target: "existing.txt"
  - for: project
    source: templates/fresh.tmpl
    target: "fresh.txt"
  - for: project
    source: templates/custom.tmpl
    target: "custom.txt"
    overwrite: false
`,
		"templates/existing.tmpl": `generated-existing`,
		"templates/fresh.tmpl":    `generated-fresh`,
		"templates/custom.tmpl":   `generated-custom`,
	})
	r, err := New(bridge, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	existing := filepath.Join(out, "existing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatalf("write prior output: %v", err)
	}
	custom := filepath.Join(out, "custom.txt")
	setBeforeCommitHook(func() {
		if err := os.WriteFile(custom, []byte("developer-owned"), 0o644); err != nil {
			t.Errorf("create custom file before commit: %v", err)
		}
	})
	t.Cleanup(func() { setBeforeCommitHook(nil) })

	written, manifest, err := r.Render(&ir.Project{Name: "P"}, out)
	if err == nil {
		t.Fatal("Render succeeded after a custom file appeared before commit")
	}
	if written != nil || manifest != nil {
		t.Errorf("failed render returned paths=%q manifest=%+v, want both nil", written, manifest)
	}
	if !strings.Contains(err.Error(), "custom.txt") || !strings.Contains(err.Error(), "preserved") {
		t.Errorf("error = %q, want it to name the preserved custom file", err)
	}

	data, err := os.ReadFile(custom)
	if err != nil || string(data) != "developer-owned" {
		t.Errorf("custom file was replaced: data=%q err=%v", data, err)
	}
	data, err = os.ReadFile(existing)
	if err != nil || string(data) != "original" {
		t.Errorf("earlier committed write not rolled back: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(out, "fresh.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("newly committed file survived rollback: %v", err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".domaincraft-render-") || strings.HasSuffix(name, ".bak") {
			t.Errorf("leftover phase artifact: %s", name)
		}
	}
}
