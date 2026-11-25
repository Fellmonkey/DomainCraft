package appdir

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func setBaseForTest(t *testing.T, dir string) {
	t.Helper()
	old := currentBaseOverride()
	SetBaseForTesting(dir)
	t.Cleanup(func() { SetBaseForTesting(old) })
}

// TestBaseFallback pins the home-directory fallback. Every cache path derives
// from Base, so a regression here redirects caches silently.
func TestBaseFallback(t *testing.T) {
	setBaseForTest(t, "")

	t.Run("from home env", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		got, err := Base()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, ".domaincraft"); got != want {
			t.Errorf("Base() = %q, want %q", got, want)
		}
	})

	t.Run("no home", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
		got, err := Base()
		if err != nil {
			t.Fatal(err)
		}
		if got != ".domaincraft" {
			t.Errorf("Base() = %q, want .domaincraft", got)
		}
	})
}

// TestBaseOverrideConcurrentAccess would report a data race under -race if
// SetBaseForTesting and Base touched an unsynchronized global.
func TestBaseOverrideConcurrentAccess(t *testing.T) {
	setBaseForTest(t, t.TempDir())
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = Base()
		}()
		go func(i int) {
			defer wg.Done()
			SetBaseForTesting(filepath.Join("parallel", strconv.Itoa(i)))
		}(i)
	}
	wg.Wait()
	SetBaseForTesting("final")
	got, err := Base()
	if err != nil || got != "final" {
		t.Fatalf("Base() after concurrent access = %q, %v; want final", got, err)
	}
}

// TestDerivedPaths checks the test seam and both path constructors: the suite
// redirects the base, so no test may write into the real ~/.domaincraft.
func TestDerivedPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	setBaseForTest(t, dir)

	t.Run("override", func(t *testing.T) {
		got, err := Base()
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Errorf("Base() = %q, want %q", got, dir)
		}
	})

	t.Run("union dir appends components", func(t *testing.T) {
		got, err := UnionDir("bridges", "cache")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(dir, "bridges", "cache"); got != want {
			t.Errorf("UnionDir() = %q, want %q", got, want)
		}
	})

	t.Run("cache dir is namespaced", func(t *testing.T) {
		got, err := CacheDir("csharp-rest", "v2")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(dir, "cache", "csharp-rest", "v2"); got != want {
			t.Errorf("CacheDir() = %q, want %q", got, want)
		}
	})
}
