package bridge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
)

// CachePath returns the local cache directory for a bridge entry.
func CachePath(entry RegistryEntry) string {
	return filepath.Join(appdir.UnionDir("bridges"), entry.ID)
}

// cacheIDForGitHub derives a cache-safe ID for a GitHub shorthand. Using the
// bare repository name would make alice/utils and bob/utils share one cache
// directory (and one checkout, including its migrations:). The owner is part
// of the key, with "__" as an unambiguous separator that keeps validateCacheID
// happy: no separators, no dot segments, a single local path component.
func cacheIDForGitHub(repo string) string {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return repo
	}
	return owner + "__" + name
}

// cacheSourceMatches reports whether an existing checkout belongs to the
// expected GitHub repository. A foreign cache is replaced, never reused: it
// can carry another owner's templates, migrations and metadata.
func cacheSourceMatches(entry RegistryEntry) bool {
	meta, err := ReadMeta(entry)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// A corrupt or unreadable metadata file is not proof of ownership —
		// but neither is proof of foreign ownership. The checkout's origin can
		// still prove it; on success rebuild the metadata instead of letting
		// the caller discard a working checkout and re-clone it.
		if !cacheOriginMatches(entry) {
			return false
		}
		// Metadata is advisory: proven ownership must not be thrown away
		// because the rewrite failed. A later run retries the rewrite.
		_ = WriteMeta(entry, newMetaFromCache(entry))
		return true
	}
	if meta == nil {
		// Legacy cache without metadata: fall back to git's origin. Fail closed
		// when there is no remote or it names a different repository.
		return cacheOriginMatches(entry)
	}
	if meta.Source == "" {
		return false // metadata without a source cannot prove ownership
	}
	return meta.Source == entry.GitHub
}

// cacheOriginMatches checks the checkout's git origin against the expected
// repository. It is the ownership proof for legacy caches and for metadata
// that could not be read. Fail closed on a missing or foreign origin.
func cacheOriginMatches(entry RegistryEntry) bool {
	out, err := runGit(CachePath(entry), "remote", "get-url", "origin")
	if err != nil {
		return false
	}
	return githubOriginMatches(out, entry.GitHub)
}

// githubHost is the canonical host for bridge sources. Origin checks, redirect
// policy and clone URL construction all derive from it so a host change cannot
// leave one of them pointing elsewhere.
const githubHost = "github.com"

// githubOriginMatches compares a git origin with the expected GitHub slug.
// A substring check would accept an unrelated repository whose name merely
// contains the expected one.
func githubOriginMatches(origin, repo string) bool {
	origin = strings.TrimSuffix(strings.TrimSpace(origin), ".git")
	for _, prefix := range []string{
		"https://" + githubHost + "/",
		"http://" + githubHost + "/",
		"ssh://git@" + githubHost + "/",
		"git@" + githubHost + ":",
		"git://" + githubHost + "/",
	} {
		if origin == prefix+repo {
			return true
		}
	}
	return false
}

// validateCacheID rejects bridge IDs that could resolve outside the cache
// directory. IDs come from registry entries and from `owner/repo` refs, so a
// separator or a dot segment would otherwise let CloneBridge and ApplyUpdate
// write to or delete an arbitrary path under the user's account.
func validateCacheID(id string) error {
	if id == "" || id == "." || id == ".." || !filepath.IsLocal(id) || filepath.Base(id) != id || strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("unsafe bridge id %q", id)
	}
	return nil
}

// IsCached checks whether a bridge is already cloned and contains bridge.yaml.
func IsCached(entry RegistryEntry) bool {
	bridgeFile := filepath.Join(CachePath(entry), "bridge.yaml")
	_, err := os.Stat(bridgeFile)
	return err == nil
}

// EnsureOptions controls how a cached bridge is kept fresh. A nil value
// performs no update checks (the cached copy, if any, is used as-is).
type EnsureOptions struct {
	// Interval is how often the remote is polled for newer versions.
	//   - positive: contact the remote only when last checked longer ago
	//     than Interval (avoids a network round-trip on every invocation)
	//   - 0: use DefaultUpdateInterval
	//   - negative: disable update checks entirely
	Interval time.Duration

	// Force makes the cache check for and honour updates immediately,
	// bypassing the freshness gate in Interval. Non-interactive CI uses this
	// via --update-bridges to pull in the latest templates deterministically.
	Force bool

	// ConfirmUpdate is invoked when a newer version of a cached bridge is
	// detected. Returning true downloads the update; returning false keeps the
	// cached copy (the user made an informed choice, so no warning is shown).
	// When nil, the update is never applied and a warning is emitted instead.
	ConfirmUpdate func(entry RegistryEntry, update *Update) (bool, error)

	// Warn receives non-fatal notices (e.g. "update available, not applied").
	// May be nil to suppress them.
	Warn func(format string, args ...any)
}

// EnsureBridge clones the bridge from GitHub if not already cached, then keeps
// the cached copy fresh by checking the upstream repo for newer versions.
// Returns the local path to the bridge directory.
func EnsureBridge(entry RegistryEntry, opts *EnsureOptions) (string, error) {
	if err := validateCacheID(entry.ID); err != nil {
		return "", err
	}
	if entry.GitHub == "" {
		return "", fmt.Errorf("bridge %q has no GitHub repository configured", entry.ID)
	}

	cacheDir := CachePath(entry)
	if IsCached(entry) && !cacheSourceMatches(entry) {
		// A cache entry from a different repository must not be served as this
		// bridge: another owner's code and migration commands would be used.
		if err := os.RemoveAll(cacheDir); err != nil {
			return "", fmt.Errorf("remove foreign bridge cache %q: %w", cacheDir, err)
		}
	}
	if !IsCached(entry) {
		// Offline fallback: materialize the embedded copy before cloning.
		ok, err := TryMaterializeEmbedded(entry, opts)
		if err != nil {
			return "", err
		}
		if ok {
			return cacheDir, nil
		}
		if err := CloneBridge(entry); err != nil {
			return "", err
		}

		// Verify the cloned repo contains bridge.yaml. Fail closed: the missing
		// manifest stays the primary error even if cleanup also fails.
		if !IsCached(entry) {
			missing := fmt.Errorf("cloned bridge %q does not contain bridge.yaml", entry.ID)
			if err := os.RemoveAll(cacheDir); err != nil {
				return "", errors.Join(missing, fmt.Errorf("remove incomplete cache %q: %w", cacheDir, err))
			}
			return "", missing
		}

		if err := WriteMeta(entry, newMetaFromCache(entry)); err != nil {
			// Metadata is advisory; the bridge is already usable.
			warnOpts(opts, "could not record cache metadata for %q: %v", entry.ID, err)
		}
		return cacheDir, nil
	}

	if opts != nil {
		// Cache is advisory: a failed freshness check must never break generation.
		if err := maybeUpdate(entry, opts); err != nil {
			warnOpts(opts, "bridge %q update check failed: %v", entry.ID, err)
		}
	}
	return cacheDir, nil
}

// maybeUpdate checks whether the cached bridge is outdated and, if so,
// consults the update policy to decide whether to download the new version.
func maybeUpdate(entry RegistryEntry, opts *EnsureOptions) error {
	interval := opts.Interval
	switch {
	case interval == 0:
		interval = DefaultUpdateInterval
	case interval < 0:
		// Update checks are disabled.
		return nil
	}
	if opts.Force {
		interval = 0 // check now, regardless of when we last looked
	}

	// A pinned bridge is deliberately fixed at one ref; refreshing it would
	// silently replace the version the user selected.
	if meta, err := ReadMeta(entry); err == nil && meta != nil && meta.Pin != "" {
		return nil
	}

	update, err := CheckForUpdate(entry, interval)
	if err != nil {
		return err
	}
	if update == nil {
		return nil // up to date (or the remote could not be reached)
	}

	// Contract gate: never offer (and thus never download) an upstream bridge
	// this core cannot support. The hard check is re-done in ApplyUpdate; this
	// only avoids prompting for an update that would be rejected anyway. A
	// failed probe is "cannot offer update", never "assume supported".
	if cerr := gateRemoteContract(entry, update.RemoteBranch); cerr != nil {
		warnOpts(opts, "skipping update of %q: %v", entry.ID, cerr)
		return nil
	}

	// No policy set: keep the cached version and surface the availability.
	if opts.ConfirmUpdate == nil {
		if opts.Warn != nil {
			opts.Warn("bridge %q (v%s) has an update available; re-run with --update-bridges to download it",
				entry.ID, update.LocalVersion)
		}
		return nil
	}

	apply, err := opts.ConfirmUpdate(entry, update)
	if err != nil {
		return err
	}
	if !apply {
		// The user chose to keep the cached version — respect the choice.
		return nil
	}

	newVersion, err := ApplyUpdate(entry, update)
	if err != nil {
		return fmt.Errorf("update bridge %q: %w", entry.ID, err)
	}
	if opts.Warn != nil {
		opts.Warn("bridge %q updated to v%s", entry.ID, newVersion)
	}
	return nil
}

// warnOpts logs through opts.Warn when the options are present.
func warnOpts(opts *EnsureOptions, format string, args ...any) {
	if opts != nil && opts.Warn != nil {
		opts.Warn(format, args...)
	}
}

// prepareCloneCacheDir checks the whole cache path for planted symlinks before
// creating the parent directory. A clone must not follow a link out of the
// cache any more than embedded materialization or metadata writes may.
func prepareCloneCacheDir(cacheDir string) error {
	cacheRoot := appdir.UnionDir("bridges")
	_, err := fsutil.AbsPathWithin(cacheRoot, cacheDir)
	if err == nil {
		err = fsutil.RefuseSymlinks(cacheRoot, cacheDir)
	}
	if err != nil {
		return fmt.Errorf("prepare bridge cache dir %q: %w", cacheDir, err)
	}
	parent := filepath.Dir(cacheDir)
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // bridge cache dirs must stay readable across CLI runs
		return fmt.Errorf("create cache directory: %w", err)
	}
	return nil
}

// CloneBridge performs a shallow git clone of the bridge repository.
func CloneBridge(entry RegistryEntry) error {
	if err := validateCacheID(entry.ID); err != nil {
		return err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required to download bridges but was not found in PATH")
	}

	cacheDir := CachePath(entry)
	if err := prepareCloneCacheDir(cacheDir); err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s/%s.git", githubHost, entry.GitHub)
	ctx, cancel := gitCommandContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, cacheDir) //nolint:gosec // args passed separately; no shell interpretation
	cmd.Env = gitScrubbedEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		cloneErr := fmt.Errorf("clone %s: %w: %s", url, err, out)
		if rerr := os.RemoveAll(cacheDir); rerr != nil {
			return errors.Join(cloneErr, fmt.Errorf("remove incomplete cache %q: %w", cacheDir, rerr))
		}
		return cloneErr
	}
	return nil
}
