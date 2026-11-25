package packages

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
)

func TestVersionStoreReportsStorageErrors(t *testing.T) {
	root := t.TempDir()
	appdir.SetBaseForTesting(root)
	defer appdir.SetBaseForTesting("")
	dir, err := appdir.CacheDir("broken-cache")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, CacheFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := versionStore("broken-cache"); err == nil {
		t.Fatal("versionStore masked a directory ReadJSON error as an empty cache")
	}
}

func TestNumericPrefixHandlesBuildMetadataAndOverflow(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		component string
		want      int
	}{
		{name: "build metadata", component: "3+build", want: 3},
		{name: "plain number", component: "12", want: 12},
		{name: "non-numeric", component: "abc", want: 0},
		{name: "overflow", component: "999999999999999999999999", want: math.MaxInt},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := numericPrefix(c.component); got != c.want {
				t.Errorf("numericPrefix(%q) = %d, want %d", c.component, got, c.want)
			}
		})
	}
}

func TestVersionGreaterWithBuildMetadata(t *testing.T) {
	t.Parallel()
	if !VersionGreater("1.2.4", "1.2.3+build") {
		t.Fatal("VersionGreater ignored the numeric part of a build-metadata component")
	}
}
