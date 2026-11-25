package fsutil

import (
	"os"
	"testing"
)

func TestLocalPathWithinRejectsEscape(t *testing.T) {
	t.Parallel()
	if _, _, err := LocalPathWithin(t.TempDir(), "../outside"); err == nil {
		t.Fatal("LocalPathWithin accepted a path outside the base")
	}
}

func TestAtomicWriteRootConfinesWrites(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := AtomicWriteRoot(root, "../outside.txt", []byte("x")); err == nil {
		t.Fatal("AtomicWriteRoot accepted a path outside the root")
	}
}
