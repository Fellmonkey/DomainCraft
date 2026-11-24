package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/renderer"
)

// R4: Save must leave a complete, parseable snapshot even when replacing an
// existing one (atomic write, no leftover temp files).
func TestSaveReplacesSnapshotWithoutTempLeftovers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := SnapshotPath(dir)

	if err := Save(path, New("demo", testProject("Product"), nil)); err != nil {
		t.Fatalf("initial Save() error = %v", err)
	}
	if err := Save(path, New("demo", testProject("Item"), nil)); err != nil {
		t.Fatalf("replacement Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("saved snapshot is not valid JSON: %v", err)
	}
	if _, ok := loaded.Entities["Item"]; !ok {
		t.Errorf("replacement snapshot lost the new entity: %+v", loaded.Entities)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read snapshot dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover snapshot temp file: %s", e.Name())
		}
	}
}

// R5 (content): replacement text must never be rescanned, so an entity renamed
// to a name containing the old name cannot double-apply the plural variant.
func TestRewriteEntityContentDoesNotRescanReplacement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{
			name: "plural and singular",
			in:   "Products Product ProductService",
			want: "ProductItems ProductItem ProductItemService",
		},
		{
			name: "camel forms",
			in:   "var products = productService;",
			want: "var productItems = productItemService;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(RewriteEntityContent([]byte(tt.in), "Product", "ProductItem"))
			if got != tt.want {
				t.Errorf("rewrite\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// R5 (path): RenameRelPath uses the same single-pass matcher, so plural/singular
// replacements in file paths cannot be applied twice.
func TestRenameRelPathDoesNotRescanReplacement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{
			name: "plural controller",
			in:   "src/WebApi/Controllers/ProductsController.cs",
			want: "src/WebApi/Controllers/ProductItemsController.cs",
		},
		{
			name: "singular entity",
			in:   "src/Domain/Entities/Product.cs",
			want: "src/Domain/Entities/ProductItem.cs",
		},
		{
			name: "singular service",
			in:   "src/Services/ProductService.cs",
			want: "src/Services/ProductItemService.cs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RenameRelPath(tt.in, "Product", "ProductItem"); got != tt.want {
				t.Errorf("RenameRelPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// R8: identifier boundaries must be rune-aware. Non-ASCII letters before the
// old token are identifier characters, so "caféProduct" must not be rewritten.
func TestRewriteEntityContentUnicodeIdentifierBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{
			name: "latin accent before token",
			in:   "var caféProduct = 1; var Product = 2;",
			want: "var caféProduct = 1; var Item = 2;",
		},
		{
			name: "cyrillic before token",
			in:   "var приветProduct = 1; var Product = 2;",
			want: "var приветProduct = 1; var Item = 2;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(RewriteEntityContent([]byte(tt.in), "Product", "Item"))
			if got != tt.want {
				t.Errorf("rewrite\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// R6: --prune follows symlinks, so a snapshot path through a symlink that
// leaves the output tree must be rejected even though it is lexically local.
func TestSafeRelPathRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if _, err := safeRelPath(root, "escape/victim.txt"); err == nil {
		t.Fatal("safeRelPath accepted a path that follows a symlink outside outputDir")
	}
}

// R7: every snapshot-supplied path (orphan files, custom files and namespace
// checks) goes through safeRelPath before os.Stat/os.ReadFile.
func TestComputeDiffIgnoresSnapshotPathsThroughSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	t.Run("orphan file list", func(t *testing.T) {
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatalf("write victim: %v", err)
		}
		old := &Snapshot{
			Entities: map[string]EntityState{"Product": {Fields: map[string]string{"id": "uuid"}}},
			Files: []renderer.RenderedFile{
				{Path: "escape/victim.txt", Entity: "Product", Custom: true},
			},
		}
		diff := ComputeDiff(old, testProject("User"), root)
		if len(diff.Deleted) != 1 {
			t.Fatalf("got %d deleted entities, want 1", len(diff.Deleted))
		}
		if len(diff.Deleted[0].Files) != 0 {
			t.Errorf("symlink-escaped file leaked into orphan list: %+v", diff.Deleted[0].Files)
		}
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		if err := os.WriteFile(victim, []byte("namespace OldNs.App;"), 0o644); err != nil {
			t.Fatalf("write victim: %v", err)
		}
		old := &Snapshot{
			ProjectNamespace: "OldNs",
			Files: []renderer.RenderedFile{
				{Path: "escape/victim.txt", Custom: true},
			},
		}
		project := testProject("User")
		project.Name = "NewNs"
		if n := ComputeDiff(old, project, root).NamespaceRename; n != nil {
			t.Errorf("symlink-escaped file leaked into namespace mismatch: %+v", n)
		}
	})
}
