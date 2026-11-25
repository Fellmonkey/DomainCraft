// Package fsutil provides small, generic filesystem helpers shared across the
// codebase (atomic writes, JSON persistence). Keeping them here avoids
// duplicating the write-to-temp-then-rename pattern in every cache.
package fsutil

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalPathWithin resolves rel against base and rejects anything outside base.
// It returns the absolute target and its path relative to base. Callers add
// their own symlink policy: RefuseSymlinks for writes, EvalSymlinks for reads
// that must follow a symlinked root.
func LocalPathWithin(base, rel string) (string, string, error) {
	local := filepath.FromSlash(rel)
	if !filepath.IsLocal(local) {
		return "", "", fmt.Errorf("path %q is not local", rel)
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", "", fmt.Errorf("resolve base %q: %w", base, err)
	}
	absTarget := filepath.Join(absBase, local)
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q escapes %q", rel, base)
	}
	relative, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return "", "", fmt.Errorf("resolve path %q: %w", rel, err)
	}
	return absTarget, relative, nil
}

// RefuseSymlinks rejects an existing symlink at base or any component of target
// below it. Missing components are ignored; the caller creates them later and
// must re-check before committing a file.
func RefuseSymlinks(base, target string) error {
	return RefuseSymlinksCached(base, target, nil)
}

// RefuseSymlinksCached is RefuseSymlinks with an optional cache of directories
// already verified non-symlink. A cached directory skips only its Lstat; the
// final target component is always inspected, and so is every missing
// component (it may have appeared since the previous call). Callers must only
// share a cache between checks whose gap they re-check separately — passing
// nil gives the fully fresh walk used by RefuseSymlinks.
func RefuseSymlinksCached(base, target string, verified map[string]bool) error {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", target, err)
	}
	paths := []string{base}
	cur := base
	for comp := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if comp == "" || comp == "." {
			continue
		}
		cur = filepath.Join(cur, comp)
		paths = append(paths, cur)
	}
	cached := verified != nil
	for i, path := range paths {
		// The leaf is the file about to be written (or, when target == base,
		// the base itself): a link planted there must never be cached away.
		isLeaf := cached && i == len(paths)-1
		if cached && !isLeaf && verified[filepath.Clean(path)] {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to write through symlink %q", path)
		}
		// Only existing, non-symlink directories enter the cache; missing
		// components stay unchecked until they appear on a later call.
		if cached && !isLeaf && info.IsDir() {
			verified[filepath.Clean(path)] = true
		}
	}
	return nil
}

// AtomicWrite writes data to path via a temp file in the same directory
// followed by a rename, so a crash mid-write never leaves a truncated or
// corrupt file behind. The parent directory is created if missing. An existing
// target keeps its permissions; a genuinely new file gets CreateTemp's 0600.
func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // generated project and cache dirs must stay world-readable
		return err
	}

	mode := os.FileMode(0o600) // CreateTemp's default for new files
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	// CreateTemp uses 0600; apply the target's mode before committing the
	// rename, the same way the renderer stages generated files.
	if err := tmp.Chmod(mode); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// AtomicWriteRoot writes data to name relative to root via a temp file and a
// rename, so a crash mid-write cannot leave a truncated file behind. Unlike
// AtomicWrite, every operation is confined to root: a symlink planted between
// validation and commit can redirect neither the temp file nor the rename.
func AtomicWriteRoot(root *os.Root, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	mode := os.FileMode(0o600)
	if info, err := root.Lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp := name + ".tmp-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }() // no-op once the rename succeeds

	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

// WriteJSON marshals v as indented JSON and writes it to path atomically.
func WriteJSON(path string, v any) error {
	return WriteJSONWithNewline(path, v, false)
}

// WriteJSONWithNewline marshals v as indented JSON and writes it atomically.
// The newline flag preserves the exact byte format expected by committed
// fixtures and schema files.
func WriteJSONWithNewline(path string, v any, newline bool) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if newline {
		data = append(data, '\n')
	}
	return AtomicWrite(path, data)
}

// ReadJSON reads path and unmarshals it into v. It returns os.ErrNotExist when
// the file does not exist.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from internal appdir/cache helpers
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
