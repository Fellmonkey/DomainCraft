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
	// A relative path-like `extends` belongs to the adapter that declared it,
	// not to the CLI's current working directory. The generic resolver treats
	// such refs as CWD-relative (and rejects a missing one as a hard error), so
	// join it with the adapter directory before handing it over.
	if strings.HasPrefix(ref, ".") && looksLikePath(ref) && fromDir != "" {
		local := ref
		if !filepath.IsAbs(local) {
			local = filepath.Join(fromDir, filepath.FromSlash(local))
		}
		return resolver.Resolve(local)
	}
	return resolver.Resolve(ref)
}

// walkUpFromDir visits dir and each ancestor up to the filesystem root,
// stopping as soon as visit reports a match. Both monorepo lookups share this
// walk so their search order and stopping rule cannot drift.
func walkUpFromDir(dir string, visit func(cur string) bool) {
	cur := dir
	for {
		if visit(cur) {
			return
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return
		}
		cur = parent
	}
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
	found := ""
	walkUpFromDir(dir, func(cur string) bool {
		candidate := filepath.Join(cur, "bridges", filepath.FromSlash(relPath))
		if info, err := os.Stat(filepath.Join(candidate, "bridge.yaml")); err == nil && !info.IsDir() {
			found = candidate
			return true
		}
		return false
	})
	return found
}

// monorepoRoot walks up from dir looking for the root of the DomainCraft
// monorepo: a directory with a bridges/ tree and a go.mod declaring this module.
// It returns "" when dir is not inside such a tree.
func monorepoRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	found := ""
	walkUpFromDir(abs, func(cur string) bool {
		if info, err := os.Stat(filepath.Join(cur, "bridges")); err == nil && info.IsDir() {
			if data, err := os.ReadFile(filepath.Join(cur, "go.mod")); err == nil && //nolint:gosec // path walked upward from the validated bridge dir
				strings.Contains(string(data), "module github.com/DomainCraft/DomainCraft") {
				found = cur
				return true
			}
		}
		return false
	})
	return found
}

// SamePath compares two paths using the filesystem's case rules. Consumers
// outside this package use it for cycle detection on case-insensitive hosts.
func SamePath(a, b string) bool {
	return samePath(a, b)
}

// samePath compares two absolute paths using the filesystem's case rules.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// MonorepoRelPath reports whether dir is the monorepo location of a bundled
// official bridge and returns its `bridges/<entryPath>` relative path, or "".
//
// Three independent checks are required: the path component before the bridge
// must be exactly `bridges` (not `mybridges`), the bridge must exist in the
// embedded tree shipped by this binary, and dir must be inside the actual
// monorepo root. A suffix match alone is not enough — a lookalike local path
// must stay a community bridge so the migration consent gate still applies.
func MonorepoRelPath(dir, entryPath string) string {
	if entryPath == "" || !filepath.IsLocal(filepath.FromSlash(entryPath)) {
		return ""
	}
	embedded := embeddedBridgeRel(entryPath)
	if _, err := domaincraft.Bridges.ReadDir(embedded); err != nil {
		return "" // not a bundled bridge in this binary
	}
	rel := filepath.Join("bridges", filepath.FromSlash(entryPath))
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	root := monorepoRoot(abs)
	if root == "" || !samePath(abs, filepath.Join(root, rel)) {
		return ""
	}
	return filepath.ToSlash(rel)
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

// Status describes where a bridge is available from and whether it is
// current. It is computed without network access unless CheckRemote is true.
type Status struct {
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
func (e RegistryEntry) Status(checkRemote bool) Status {
	s := Status{}
	if e.Path != "" {
		if _, err := domaincraft.Bridges.ReadDir(embeddedBridgeRel(e.Path)); err == nil {
			s.Embedded = true
		}
	}
	if IsCached(e) {
		s.Cached = true
	}
	if !checkRemote {
		return s
	}
	// A pinned bridge is fixed at one ref; there is no update to offer.
	if meta, err := ReadMeta(e); err == nil && meta != nil && meta.Pin != "" {
		return s
	}
	u, err := CheckForUpdate(e, 0)
	if err == nil && u != nil {
		s.RemoteReachable = true
		// Only flag an update when the remote contract can be read and is
		// supported. A failed probe is "cannot offer update".
		if c, rerr := remoteContractVersion(e, u.RemoteBranch); rerr == nil {
			if cerr := shouldApplyContract(c); cerr == nil {
				s.Outdated = true
			}
		}
	}
	return s
}
