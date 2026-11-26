package snapshot

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteEntityContent(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"pascal standalone", "using AcmeShop.Domain.Entities; var p = new Product();", "using AcmeShop.Domain.Entities; var p = new Item();"},
		{"compound service", "class ProductService : IProductService", "class ItemService : IItemService"},
		{"camel", "var product = repo;", "var item = repo;"},
		{"plural pascal", "List<Product> Products = null;", "List<Item> Items = null;"},
		{"plural camel", "var products = new list;", "var items = new list;"},
		{"does not touch longer word", "Productivity", "Productivity"},
		{"inner identifier survives", "var myProduct = 1;", "var myProduct = 1;"},
		{"single uppercase prefix", "XProductService", "XItemService"},
		{"snake token", "product_name", "item_name"},
		{"upper", "PRODUCTS", "ITEMS"},
		{"namespace untouched by entity rewrite", "namespace AcmeShop.App;", "namespace AcmeShop.App;"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(RewriteEntityContent([]byte(tc.in), "Product", "Item"))
			if got != tc.want {
				t.Errorf("RewriteEntityContent mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// Regression: a field rename declared in the diff was dropped from the
// compound transform, leaving stale Title/title/TITLE identifiers in custom
// files after an entity rename.
func TestRenameTransformsIncludesFieldRenames(t *testing.T) {
	t.Parallel()
	d := &Diff{FieldRenames: []FieldRename{{Entity: "Item", OldField: "title", NewField: "name"}}}
	content := []byte("class ItemService { public string Title; var title = 1; }")
	for _, transform := range RenameTransforms("Product", "Item", d) {
		content = transform(content)
	}
	want := "class ItemService { public string Name; var name = 1; }"
	if string(content) != want {
		t.Errorf("field rename dropped\n got: %q\nwant: %q", content, want)
	}
}

func TestRewriteFieldContent(t *testing.T) {
	t.Parallel()
	in := "entity.Title == prev.Title; var title = Title; TITLE;"
	want := "entity.Name == prev.Name; var name = Name; NAME;"
	if got := string(RewriteFieldContent([]byte(in), "title", "name")); got != want {
		t.Errorf("field rewrite\ngot: %q\nwant: %q", got, want)
	}
}

func TestRewriteNamespaceContent(t *testing.T) {
	t.Parallel()
	in := "namespace AcmeShop.Application.Services;\nusing AcmeShop.Domain.Entities;"
	want := "namespace AcmeStore.Application.Services;\nusing AcmeStore.Domain.Entities;"
	got := string(RewriteNamespaceContent([]byte(in), "AcmeShop", "AcmeStore"))
	if got != want {
		t.Errorf("namespace rewrite\ngot: %q\nwant: %q", got, want)
	}
}

func TestRewriteFileRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rel := "src/Services/ItemService.cs"
	original := "public partial class ProductService { Task<Product> GetAsync(); }"
	if err := os.MkdirAll(filepath.Join(dir, "src", "Services"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := &Diff{NamespaceRename: &NamespaceMismatch{OldNamespace: "EscrowPay", NewNamespace: "EscrowNow"}}
	rewrote, err := RewriteFile(dir, rel, RenameTransforms("Product", "Item", diff)...)
	if err != nil {
		t.Fatal(err)
	}
	if !rewrote {
		t.Fatal("expected file to be rewritten")
	}
	got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	want := "public partial class ItemService { Task<Item> GetAsync(); }"
	if string(got) != want {
		t.Errorf("round trip\ngot: %q\nwant: %q", string(got), want)
	}
}

// casingVariants is the one variant table shared by content and path rewrites.
// Pin the exact set so neither caller grows or loses a casing silently.
func TestCasingVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		old, new string
		plural   bool
		want     map[string]string
	}{
		{
			name:   "entity with plural",
			old:    "Product",
			new:    "Item",
			plural: true,
			want: map[string]string{
				"Product":  "Item",
				"product":  "item",
				"PRODUCT":  "ITEM",
				"Products": "Items",
				"products": "items",
				"PRODUCTS": "ITEMS",
			},
		},
		{
			name:   "field without plural",
			old:    "Title",
			new:    "Name",
			plural: false,
			want: map[string]string{
				"Title": "Name",
				"title": "name",
				"TITLE": "NAME",
			},
		},
		{
			name:   "namespace keeps declared casing",
			old:    "AcmeShop",
			new:    "AcmeStore",
			plural: false,
			want: map[string]string{
				"AcmeShop": "AcmeStore",
				"acmeShop": "acmeStore",
				"ACMESHOP": "ACMESTORE",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := casingVariants(tt.old, tt.new, tt.plural)
			if !maps.Equal(got, tt.want) {
				t.Errorf("casingVariants(%q, %q, %v) = %v, want %v", tt.old, tt.new, tt.plural, got, tt.want)
			}
		})
	}
}

// A RewriteFile pass rewrites a developer-owned file in place. The atomic
// write must not change its permissions. Windows normalises permission bits
// (0644 reads back as 0666), so compare the observed before/after mode.
func TestRewriteFilePreservesExistingMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rel := "src/Services/ItemService.cs"
	abs := filepath.Join(dir, "src", "Services", "ItemService.cs")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("class ProductService {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(abs)
	if err != nil {
		t.Fatal(err)
	}
	modeBefore := before.Mode().Perm()

	rewrote, err := RewriteFile(dir, rel, EntityTransform("Product", "Item"))
	if err != nil {
		t.Fatal(err)
	}
	if !rewrote {
		t.Fatal("expected file to be rewritten")
	}
	after, err := os.Lstat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Mode().Perm(); got != modeBefore {
		t.Errorf("mode = %04o, want %04o (unchanged from the 0644 seed)", got, modeBefore)
	}
}

func TestRewriteFileNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rel := "a.cs"
	content := "var unrelated = 1;"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rewrote, err := RewriteFile(dir, rel, EntityTransform("Product", "Item"))
	if err != nil {
		t.Fatal(err)
	}
	if rewrote {
		t.Fatal("file should not be rewritten when nothing matched")
	}
	got, _ := os.ReadFile(filepath.Join(dir, rel))
	if string(got) != content {
		t.Errorf("file changed: %q", string(got))
	}
}

func TestRewriteFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rewrote, err := RewriteFile(dir, "does/not/exist.cs", EntityTransform("Product", "Item"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if rewrote {
		t.Fatal("missing file must not be reported as rewritten")
	}
}
