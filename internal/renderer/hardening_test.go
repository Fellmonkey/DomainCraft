package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

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

	err := rollbackWrites(nil, []stagedWrite{{
		target:   target,
		original: []byte("original"),
		existed:  true,
		mode:     0o644,
	}})
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
	if rollbackError(primary, nil) != primary {
		t.Error("no-op rollback must return the original error unchanged")
	}
}
