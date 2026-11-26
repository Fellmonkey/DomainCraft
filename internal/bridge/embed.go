package bridge

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	domaincraft "github.com/DomainCraft/DomainCraft"
	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
)

// embeddedBridgeRel builds the path of a bundled bridge inside the embedded
// filesystem. All embedded-tree lookups use this one spelling.
func embeddedBridgeRel(entryPath string) string {
	return "bridges/" + filepath.ToSlash(entryPath)
}

// TryMaterializeEmbedded copies the embedded copy of an official bridge into its
// cache directory. It returns (true, nil) when the bridge is embedded and was
// materialized, (false, nil) when it is not embedded (caller should clone), and
// (false, err) on an IO failure.
//
// This is the offline fallback in the loader order (local path → embedded →
// remote → cache): an installed binary can generate code for an official bridge
// without contacting GitHub, because the bridge ships inside the binary.
func TryMaterializeEmbedded(entry RegistryEntry, opts *EnsureOptions) (bool, error) {
	if entry.Path == "" {
		return false, nil
	}
	rel := embeddedBridgeRel(entry.Path)
	if _, err := domaincraft.Bridges.ReadDir(rel); err != nil {
		return false, nil // not embedded in this binary
	}

	dst := CachePath(entry)
	// The cache may be attacker-controlled (a planted symlink would redirect
	// writes outside it), so check the whole path before creating anything.
	cacheRoot := appdir.UnionDir("bridges")
	_, err := fsutil.AbsPathWithin(cacheRoot, dst)
	if err == nil {
		err = fsutil.RefuseSymlinks(cacheRoot, dst)
	}
	if err != nil {
		return false, fmt.Errorf("materialize embedded bridge %q: %w", entry.ID, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil { //nolint:gosec // bridge cache dirs must stay readable across CLI runs
		return false, fmt.Errorf("create cache dir for embedded bridge %q: %w", entry.ID, err)
	}
	if err := writeFSDir(domaincraft.Bridges, rel, dst); err != nil {
		return false, fmt.Errorf("materialize embedded bridge %q: %w", entry.ID, err)
	}
	// Record metadata as for any cached checkout. The version comes from
	// bridge.yaml; there is no git commit/branch for an embedded copy.
	if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
		warnOpts(opts, "could not record cache metadata for embedded bridge %q: %v", entry.ID, err)
	}
	return true, nil
}

// writeFSDir copies every file in the embedded filesystem subtree rooted at
// root into the destination directory, preserving relative paths. Every target
// component is checked for symlinks before it is written.
func writeFSDir(fsys fs.FS, root, dst string) error {
	// os.Root confines every write to dst. RefuseSymlinks still rejects links
	// that stay inside the cache; Root additionally closes the window between
	// the check and the commit if a component is replaced concurrently.
	dir, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		target := filepath.Join(dst, filepath.FromSlash(rel))
		_, err = fsutil.AbsPathWithin(dst, target)
		if err == nil {
			err = fsutil.RefuseSymlinks(dst, target)
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(rel)
		if name == "" {
			return nil
		}
		if d.IsDir() {
			return dir.MkdirAll(name, 0o755)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		return fsutil.AtomicWriteRoot(dir, name, data)
	})
}
