package fsutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteCreatesDirsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "file.txt")

	if err := AtomicWrite(path, []byte("hello")); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

// A rewrite must not silently turn a developer-owned 0644 file into a 0600
// file: AtomicWrite stages in a 0600 temp file, so it has to copy the target's
// mode onto the temp file before the rename. Windows normalises permission bits
// (0644 reads back as 0666), so the assertion compares the observed mode.
func TestAtomicWritePreservesExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "developer-owned.cs")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat before: %v", err)
	}
	modeBefore := before.Mode().Perm()

	if err := AtomicWrite(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat after: %v", err)
	}
	if got := after.Mode().Perm(); got != modeBefore {
		t.Errorf("mode = %04o, want %04o (unchanged from the 0644 seed)", got, modeBefore)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
}

func TestAtomicWriteOverwritesAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	if err := AtomicWrite(path, []byte(`{"v":1}`)); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := AtomicWrite(path, []byte(`{"v":2}`)); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != `{"v":2}` {
		t.Errorf("content = %q, want the second write to win", data)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "cache.json" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteJSONReadJSONRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")

	type payload struct {
		ID      string   `json:"id"`
		Version string   `json:"version"`
		Tags    []string `json:"tags"`
	}
	in := payload{ID: "csharp-restful", Version: "1.2.3", Tags: []string{"a", "b"}}

	if err := WriteJSON(path, in); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var out payload
	if err := ReadJSON(path, &out); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if out.ID != in.ID || out.Version != in.Version || len(out.Tags) != len(in.Tags) || out.Tags[0] != in.Tags[0] {
		t.Errorf("roundtrip = %+v, want %+v", out, in)
	}
}

func TestWriteJSONRejectsUnmarshalableValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := WriteJSON(path, map[string]any{"ch": make(chan int)}); err == nil {
		t.Fatal("expected an error for an unmarshalable value")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed write should not leave a file behind, got %v", err)
	}
}

func TestReadJSONMissingFileReturnsNotExist(t *testing.T) {
	var v map[string]string
	err := ReadJSON(filepath.Join(t.TempDir(), "absent.json"), &v)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestReadJSONCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var v map[string]string
	var syn *json.SyntaxError
	if err := ReadJSON(path, &v); !errors.As(err, &syn) {
		t.Errorf("err = %v, want a JSON syntax error", err)
	}
}

// A verified parent must not make the file leaf trustworthy: the leaf is
// re-inspected on every call, including when it was never in the cache.
func TestRefuseSymlinksCachedRejectsSymlinkAtFinalComponent(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	verified := make(map[string]bool)
	cleanTarget := filepath.Join(sub, "clean.txt")
	if err := RefuseSymlinksCached(base, cleanTarget, verified); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	if !verified[filepath.Clean(base)] || !verified[filepath.Clean(sub)] {
		t.Fatalf("base and sub should be cached as verified directories, got %v", verified)
	}
	if verified[filepath.Clean(cleanTarget)] {
		t.Fatal("final target component must never be cached")
	}

	link := filepath.Join(sub, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	want := fmt.Sprintf("refuse to write through symlink %q", link)
	if got := RefuseSymlinksCached(base, link, verified); got == nil || got.Error() != want {
		t.Errorf("cached check = %v, want %q", got, want)
	}
	if got := RefuseSymlinks(base, link); got == nil || got.Error() != want {
		t.Errorf("fresh check = %v, want %q", got, want)
	}
	if verified[filepath.Clean(link)] {
		t.Error("symlinked leaf was cached")
	}
}

// Cached and uncached checks must be indistinguishable for clean trees and
// for a symlink at an intermediate component.
func TestRefuseSymlinksCachedMatchesRefuseSymlinks(t *testing.T) {
	tests := []struct {
		name   string
		target func(t *testing.T, base string) string
	}{
		{
			name: "clean tree",
			target: func(t *testing.T, base string) string {
				t.Helper()
				dir := filepath.Join(base, "src")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir src: %v", err)
				}
				leaf := filepath.Join(dir, "file.txt")
				if err := os.WriteFile(leaf, []byte("x"), 0o644); err != nil {
					t.Fatalf("write leaf: %v", err)
				}
				return leaf
			},
		},
		{
			name: "symlinked intermediate component",
			target: func(t *testing.T, base string) string {
				t.Helper()
				realDir := filepath.Join(base, "real")
				if err := os.MkdirAll(realDir, 0o755); err != nil {
					t.Fatalf("mkdir real: %v", err)
				}
				link := filepath.Join(base, "linked")
				if err := os.Symlink(realDir, link); err != nil {
					t.Skipf("symlinks unavailable on this host: %v", err)
				}
				return filepath.Join(link, "file.txt")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			target := tt.target(t, base)
			fresh := fmt.Sprintf("%v", RefuseSymlinks(base, target))

			verified := make(map[string]bool)
			for range 2 { // the second call must use the cache
				got := fmt.Sprintf("%v", RefuseSymlinksCached(base, target, verified))
				if got != fresh {
					t.Errorf("cached check = %q, fresh check = %q", got, fresh)
				}
			}
		})
	}
}

// A component that was missing on the first call is never cached: when it
// later appears as a symlink, the next call must still reject it.
func TestRefuseSymlinksCachedRechecksNewlyCreatedComponent(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	missing := filepath.Join(base, "later")
	target := filepath.Join(missing, "file.txt")

	verified := make(map[string]bool)
	if err := RefuseSymlinksCached(base, target, verified); err != nil {
		t.Fatalf("check with missing component: %v", err)
	}
	if verified[filepath.Clean(missing)] {
		t.Fatal("missing component must not be cached")
	}
	if err := os.Symlink(outside, missing); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	want := fmt.Sprintf("refuse to write through symlink %q", missing)
	if got := RefuseSymlinksCached(base, target, verified); got == nil || got.Error() != want {
		t.Errorf("cached check = %v, want %q", got, want)
	}
}

func TestAtomicWriteRootRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer func() { _ = dir.Close() }()
	if err := AtomicWriteRoot(dir, filepath.Join("escape", "victim.txt"), []byte("bad")); err == nil {
		t.Fatal("AtomicWriteRoot followed a symlink outside the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "victim.txt")); !os.IsNotExist(err) {
		t.Errorf("file appeared outside the root: err = %v", err)
	}
}
