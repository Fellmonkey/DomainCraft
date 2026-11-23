package bridge

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	domaincraft "github.com/DomainCraft/DomainCraft"
)

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
	rel := "bridges/" + filepath.ToSlash(entry.Path)
	if _, err := domaincraft.Bridges.ReadDir(rel); err != nil {
		return false, nil // not embedded in this binary
	}

	dst := CachePath(entry)
	if err := os.MkdirAll(dst, 0o755); err != nil {
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
// root into the destination directory, preserving relative paths.
func writeFSDir(fsys fs.FS, root, dst string) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, "/")
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
