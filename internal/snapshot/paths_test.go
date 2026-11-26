package snapshot

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeRelPathKeepsPathsInsideOutputDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "plain path", path: "src/File.cs"},
		{name: "dot", path: "."},
		{name: "parent escape", path: "../../.ssh/authorized_keys", wantErr: true},
		{name: "absolute path", path: "/etc/passwd", wantErr: true},
	}

	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name    string
			path    string
			wantErr bool
		}{name: "drive path", path: `C:\Users\secret`, wantErr: true})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := safeRelPath(root, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("safeRelPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if err == nil && !strings.HasPrefix(got, absRoot+string(filepath.Separator)) && got != absRoot {
				t.Fatalf("safeRelPath(%q) = %q, outside %q", tt.path, got, absRoot)
			}
		})
	}
}

func TestDeleteFileRejectsSnapshotEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	victim := filepath.Join(filepath.Dir(root), "dc-victim-"+filepath.Base(root)+".txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := DeleteFile(root, "../"+filepath.Base(victim))
	if err == nil {
		t.Fatal("DeleteFile accepted a path outside outputDir")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "keep" {
		t.Fatalf("victim changed: data=%q err=%v", data, err)
	}
}

func TestReplaceScaffoldedFileRejectsSnapshotEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	victim := filepath.Join(filepath.Dir(root), "dc-replace-victim-"+filepath.Base(root)+".txt")
	if err := os.WriteFile(victim, []byte("Product"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ReplaceScaffoldedFile(root, "../"+filepath.Base(victim), "dest.txt", EntityTransform("Product", "Item"))
	if err == nil {
		t.Fatal("ReplaceScaffoldedFile accepted a source path outside outputDir")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "Product" {
		t.Fatalf("victim changed: data=%q err=%v", data, err)
	}
}

func TestRewriteFileRejectsSnapshotEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	victim := filepath.Join(filepath.Dir(root), "dc-rewrite-victim-"+filepath.Base(root)+".txt")
	if err := os.WriteFile(victim, []byte("Product"), 0o600); err != nil {
		t.Fatal(err)
	}

	written, err := RewriteFile(root, "../"+filepath.Base(victim), EntityTransform("Product", "Item"))
	if err == nil || written {
		t.Fatalf("RewriteFile accepted a path outside outputDir: written=%v err=%v", written, err)
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "Product" {
		t.Fatalf("victim changed: data=%q err=%v", data, err)
	}
}
