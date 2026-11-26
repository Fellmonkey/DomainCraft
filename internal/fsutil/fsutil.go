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

// AbsPathWithin checks that an absolute target stays under base and returns its
// relative form. It is the containment half shared by lexical path resolution
// and by callers that already hold an absolute path (bridge cache materialization).
func AbsPathWithin(base, target string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve base %q: %w", base, err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve target %q: %w", target, err)
	}
	return relWithin(absBase, absTarget, target)
}

// relWithin is the containment check for callers that already hold absolute
// paths. Keeping it separate avoids a second filepath.Abs on the same base.
func relWithin(absBase, absTarget, displayTarget string) (string, error) {
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", displayTarget, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes %q", displayTarget, absBase)
	}
	return rel, nil
}

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
	relative, err := relWithin(absBase, absTarget, absTarget)
	if err != nil {
		return "", "", err
	}
	return absTarget, relative, nil
}

// OpenRootFor is the shared root transaction: containment, symlink refusal and
// one os.OpenRoot on the verified base. Every read or write goes through the
// returned root, so a component swapped for a symlink after the check cannot
// redirect it. The caller closes the root. Opening one root per operation keeps
// the cost at ~1.13x versus ~2.9x for routing every staging call through Root.
func OpenRootFor(base, absTarget string) (*os.Root, string, error) {
	rel, err := AbsPathWithin(base, absTarget)
	if err != nil {
		return nil, "", err
	}
	if err := RefuseSymlinks(base, absTarget); err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", fmt.Errorf("open root %q: %w", base, err)
	}
	return root, rel, nil
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

// atomicOps is the filesystem surface shared by AtomicWrite and
// AtomicWriteRoot. osAtomicOps routes through os.*, rootAtomicOps through one
// *os.Root, so the staging algorithm below exists only once.
type atomicOps interface {
	mkdir(dir string) error
	lstat(name string) (os.FileInfo, error)
	createTemp(dir, name string) (*os.File, string, error)
	rename(from, to string) error
	remove(name string) error
	// abort closes a file after a staging failure and shapes the returned
	// error. os.* joins the close error, *os.Root drops it; keeping the policy
	// in the backend preserves both callers' error contracts.
	abort(f *os.File, err error) error
}

type osAtomicOps struct{}

func (osAtomicOps) mkdir(dir string) error {
	return os.MkdirAll(dir, 0o755) //nolint:gosec // generated project and cache dirs must stay world-readable
}

func (osAtomicOps) lstat(name string) (os.FileInfo, error) { return os.Lstat(name) }

func (osAtomicOps) createTemp(dir, name string) (*os.File, string, error) {
	f, err := os.CreateTemp(dir, filepath.Base(name)+".tmp-*")
	if err != nil {
		return nil, "", err
	}
	return f, f.Name(), nil
}

func (osAtomicOps) rename(from, to string) error { return os.Rename(from, to) }

func (osAtomicOps) remove(name string) error { return os.Remove(name) }

func (osAtomicOps) abort(f *os.File, err error) error { return errors.Join(err, f.Close()) }

type rootAtomicOps struct{ root *os.Root }

func (o rootAtomicOps) mkdir(dir string) error { return o.root.MkdirAll(dir, 0o755) }

func (o rootAtomicOps) lstat(name string) (os.FileInfo, error) { return o.root.Lstat(name) }

func (o rootAtomicOps) createTemp(_, name string) (*os.File, string, error) {
	tmp := name + ".tmp-" + rand.Text()
	f, err := o.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, tmp, nil
}

func (o rootAtomicOps) rename(from, to string) error { return o.root.Rename(from, to) }

func (o rootAtomicOps) remove(name string) error { return o.root.Remove(name) }

func (rootAtomicOps) abort(f *os.File, err error) error {
	_ = f.Close()
	return err
}

// atomicWrite stages data in a temp file in the target's directory, copies the
// target's mode onto it, then renames it into place. Every filesystem call goes
// through ops, so the same algorithm can be confined to an *os.Root. A crash
// before the rename leaves an orphaned temp file, never a truncated target.
func atomicWrite(ops atomicOps, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := ops.mkdir(dir); err != nil {
		return err
	}

	mode := os.FileMode(0o600) // CreateTemp/OpenFile's default for new files
	if info, err := ops.lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	f, tmpName, err := ops.createTemp(dir, name)
	if err != nil {
		return err
	}
	defer func() { _ = ops.remove(tmpName) }() // no-op once the rename succeeds

	// The temp file starts at 0600; apply the target's mode before committing
	// the rename, the same way the renderer stages generated files.
	if err := f.Chmod(mode); err != nil {
		return ops.abort(f, err)
	}
	if _, err := f.Write(data); err != nil {
		return ops.abort(f, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return ops.rename(tmpName, name)
}

// AtomicWrite writes data to path via a temp file in the same directory
// followed by a rename, so a crash mid-write never leaves a truncated or
// corrupt file behind. The parent directory is created if missing. An existing
// target keeps its permissions; a genuinely new file gets CreateTemp's 0600.
func AtomicWrite(path string, data []byte) error {
	return atomicWrite(osAtomicOps{}, path, data)
}

// AtomicWriteRoot writes data to name relative to root via a temp file and a
// rename, so a crash mid-write cannot leave a truncated file behind. Unlike
// AtomicWrite, every operation is confined to root: a symlink planted between
// validation and commit can redirect neither the temp file nor the rename.
func AtomicWriteRoot(root *os.Root, name string, data []byte) error {
	return atomicWrite(rootAtomicOps{root: root}, name, data)
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
		return fmt.Errorf("marshal JSON for %q: %w", path, err)
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
