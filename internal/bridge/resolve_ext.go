package bridge

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveExtendsRef resolves one `extends` reference. It shares the loader
// order with the top-level `--bridge` entry, from most to least preferred:
//
// First, the monorepo layout: walk up from the extending bridge looking for
// <root>/bridges/<entry.Path>. The ID→path mapping is registry data, never
// inferred from the directory name.
//
// Second, the legacy layouts: a repo-named checkout next to the extending
// bridge (monorepo sibling) or inside its directory (a CI checkout), so
// development works before the base bridge is published.
//
// Finally, the normal path/ID/owner-repo resolution (cache + clone).
func ResolveExtendsRef(ref, fromDir string, resolver *Resolver) (string, error) {
	if entry := Default().ByID(ref); entry != nil {
		if p := FindMonorepoBridge(fromDir, entry.Path); p != "" {
			return p, nil
		}
		repo := filepath.Base(entry.GitHub)
		for _, candidate := range []string{
			filepath.Join(filepath.Dir(fromDir), repo), // sibling of the adapter (monorepo)
			filepath.Join(fromDir, repo),               // inside the adapter's directory (CI checkout)
		} {
			if info, err := os.Stat(filepath.Join(candidate, "bridge.yaml")); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return resolver.Resolve(ref)
}

// FindMonorepoBridge walks up from dir looking for <root>/bridges/<relPath>
// containing a bridge.yaml. It returns the bridge directory, or "" when relPath
// is empty or no such bridge exists anywhere up the tree. The walk stops at the
// filesystem root; the upward search is safe because an unrelated
// `<root>/bridges` higher up is the explicit, documented monorepo convention.
func FindMonorepoBridge(dir, relPath string) string {
	if relPath == "" {
		return ""
	}
	cur := dir
	for {
		candidate := filepath.Join(cur, "bridges", filepath.FromSlash(relPath))
		if info, err := os.Stat(filepath.Join(candidate, "bridge.yaml")); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// MonorepoRelPath reports whether dir is the monorepo location for an official
// bridge (i.e. it ends with `bridges/<entryPath>`) and returns that relative
// path, or "" when the bridge was resolved from elsewhere.
func MonorepoRelPath(dir, entryPath string) string {
	if entryPath == "" {
		return ""
	}
	suffix := filepath.Join("bridges", filepath.FromSlash(entryPath))
	if strings.HasSuffix(filepath.Clean(dir), suffix) {
		return filepath.ToSlash(suffix)
	}
	return ""
}

// IsOfficialBundled reports whether dir is a bundled monorepo bridge, i.e. it
// matches an official registry entry's `<root>/bridges/<path>` location. A
// bridge that is not bundled (a local custom path, or a clone from an
// owner/repo outside the registry) is treated as a community bridge.
func IsOfficialBundled(dir string) bool {
	for _, e := range Default().All() {
		if e.Path != "" && MonorepoRelPath(dir, e.Path) != "" {
			return true
		}
	}
	return false
}
