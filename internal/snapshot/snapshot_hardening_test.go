package snapshot

import (
	"bytes"
	"testing"
)

func TestRewritePreservesInvalidUTF8Bytes(t *testing.T) {
	t.Parallel()

	t.Run("unrelated invalid byte survives", func(t *testing.T) {
		t.Parallel()
		content := []byte{0x61, 0xe9, 0x62}
		if out := RewriteEntityContent(content, "Product", "Item"); !bytes.Equal(out, content) {
			t.Fatalf("rewrite changed bytes: got %x, want %x", out, content)
		}
	})

	t.Run("identifiers around invalid byte are rewritten", func(t *testing.T) {
		t.Parallel()
		content := append([]byte("Product "), 0xe9, ' ', 'P', 'r', 'o', 'd', 'u', 'c', 't')
		want := append([]byte("Item "), 0xe9, ' ', 'I', 't', 'e', 'm')
		out := RewriteEntityContent(content, "Product", "Item")
		if !bytes.Equal(out, want) {
			t.Fatalf("rewrite = %x, want %x", out, want)
		}
	})
}

func TestReferencesNamespaceIsLanguageAgnostic(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		src  string
		want bool
	}{
		{name: "csharp", src: "namespace Old.Root;", want: true},
		{name: "typescript", src: `import { Api } from "Old.Root/api";`, want: true},
		{name: "python", src: "from Old.Root import api", want: true},
		{name: "different namespace", src: "namespace Other.Root;", want: false},
		{name: "embedded in identifier", src: "MyOld.RootThing", want: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := referencesNamespace(c.src, "Old.Root"); got != c.want {
				t.Fatalf("referencesNamespace(%q) = %t, want %t", c.src, got, c.want)
			}
		})
	}
}
