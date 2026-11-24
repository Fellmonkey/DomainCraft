package bridge

import (
	"os"
	"path/filepath"
	"strings"

	domaincraft "github.com/DomainCraft/DomainCraft"
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

// BridgeStatus describes where a bridge is available from and whether it is
// current. It is computed without network access unless CheckRemote is true.
type BridgeStatus struct {
	// Embedded is true when the bridge ships inside this binary (go:embed).
	Embedded bool
	// Cached is true when the bridge is materialized in the local cache.
	Cached bool
	// Outdated is true when a cached/embedded bridge has a newer remote version
	// AND that newer version is supported by the current contract. It is only
	// populated when CheckRemote is true.
	Outdated bool
	// RemoteReachable records whether the remote was contacted successfully
	// during a CheckRemote computation (false when offline/unknown).
	RemoteReachable bool
}

// Status computes a bridge's availability status.
//   - CheckRemote=false (the default for `bridges`): only local facts — embedded
//     and cached — are reported, with no network traffic.
//   - CheckRemote=true (used by `--check-updates`): additionally probes the
//     remote and reports whether a newer supported version exists.
func (e RegistryEntry) Status(checkRemote bool) BridgeStatus {
	s := BridgeStatus{}
	if e.Path != "" {
		if _, err := domaincraft.Bridges.ReadDir("bridges/" + filepath.ToSlash(e.Path)); err == nil {
			s.Embedded = true
		}
	}
	if IsCached(e) {
		s.Cached = true
	}
	if !checkRemote {
		return s
	}
	u, err := CheckForUpdate(e, 0)
	if err == nil && u != nil {
		// Only flag an update if the remote contract is supported, so an
		// unsupported (neverer-contract) bridge is never offered.
		if c, rerr := remoteContractVersion(e, u.RemoteBranch); rerr == nil {
			if cerr := shouldApplyContract(c); cerr == nil {
				s.Outdated = true
				s.RemoteReachable = true
			}
		}
	}
	return s
}
