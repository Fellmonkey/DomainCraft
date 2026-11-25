package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Resolver maps a bridge identifier to a local filesystem path.
// The identifier can be:
//   - a local path (directory or file) — used directly
//   - a registry ID (e.g. "csharp-restful") — resolved via cache/clone
//   - empty — caller should prompt the user
type Resolver struct {
	registry *Registry
	ensure   *EnsureOptions // update/caching policy; nil = no update checks
}

// NewResolver creates a resolver backed by the given registry.
func NewResolver(registry *Registry) *Resolver {
	return &Resolver{registry: registry}
}

// WithEnsureOptions returns a resolver that applies the given caching/update
// policy whenever it clones or refreshes a bridge from a registry ID.
func (r *Resolver) WithEnsureOptions(opts *EnsureOptions) *Resolver {
	r2 := *r
	r2.ensure = opts
	return &r2
}

// Resolve maps a bridge identifier to a local path containing bridge.yaml.
// Returns ("", nil) when input is empty — caller must handle interactive selection.
func (r *Resolver) Resolve(input string) (string, error) {
	if input == "" {
		return "", nil
	}

	// 1. Local path — use directly. Bare names that exist in the CWD count too.
	dir, _, err := LocalBridgePaths(input)
	if err == nil {
		return dir, nil
	}
	if _, ok := errors.AsType[*missingBridgeConfigError](err); ok {
		// The path exists but is not a bridge. That is a hard error, never a
		// registry ID or GitHub shorthand.
		return "", err
	}

	// Path-like inputs ("../x", "./x", "/x", "~/x", "C:\\x") that do not
	// exist are an error, never a GitHub shorthand: a missing "../bridge"
	// must not turn into a clone of github.com/../bridge.git.
	if looksLikePath(input) {
		abs, _ := filepath.Abs(input)
		return "", fmt.Errorf("bridge %q not found: no such local path (tried %q); for GitHub use owner/repo", input, abs)
	}

	// 2. Registry ID — check cache, clone if needed.
	if entry := r.registry.ByID(input); entry != nil {
		return EnsureBridge(*entry, r.ensure)
	}

	// 3. GitHub shorthand "owner/repo" — clone directly. An owner of "." or
	// ".." would be a path fragment, not a GitHub user (looksLikePath already
	// rejects dot-led inputs; this keeps step 3 correct on its own too).
	parts := strings.SplitN(input, "/", 2)
	isOwnerRepo := len(parts) == 2 && parts[0] != "" && parts[1] != "" && parts[0] != "." && parts[0] != ".."
	if isOwnerRepo {
		if strings.ContainsAny(parts[1], "/\\") || parts[1] == "." || parts[1] == ".." {
			return "", fmt.Errorf("bridge %q not found: invalid repository name", input)
		}
		repo, ref := splitPinRef(input)
		if ref != "" {
			if err := validatePinRef(ref); err != nil {
				return "", err
			}
		}
		entry := RegistryEntry{
			// Owner-scoped: the bare repository name would collide in the
			// cache with another owner's repo of the same name.
			ID:     cacheIDForGitHub(repo),
			GitHub: repo,
		}
		path, err := EnsureBridge(entry, r.ensure)
		if err != nil {
			return "", err
		}
		if ref != "" {
			if err := checkoutPinnedRef(path, ref); err != nil {
				return "", err
			}
			// Persist the pin: a later cache refresh must not silently move a
			// pinned bridge onto the default branch.
			if err := recordPin(entry, ref); err != nil {
				return "", err
			}
		}
		return path, nil
	}

	return "", fmt.Errorf("bridge %q not found: not a local path, registry ID, or owner/repo", input)
}

// missingBridgeConfigError marks an existing directory that has no bridge.yaml.
// Resolve surfaces it verbatim instead of falling through to registry/GitHub
// resolution; the renderer fails closed on the same error.
type missingBridgeConfigError struct{ dir string }

// Error reports the directory lacking bridge.yaml.
func (e *missingBridgeConfigError) Error() string {
	return fmt.Sprintf("directory %q does not contain bridge.yaml", e.dir)
}

// LocalBridgePaths validates a local bridge reference and returns its directory
// and bridge.yaml path. A directory must contain bridge.yaml; a file is treated
// as the config itself and yields its parent directory. Missing or unreadable
// paths are errors. This is the single normalization used by both the resolver
// and the renderer, so a path accepted by one is accepted by the other.
func LocalBridgePaths(input string) (dir, configFile string, err error) {
	info, err := os.Stat(input)
	if err != nil {
		return "", "", fmt.Errorf("bridge path %q: %w", input, err)
	}
	if !info.IsDir() {
		return filepath.Dir(input), input, nil
	}
	config := filepath.Join(input, "bridge.yaml")
	if _, err := os.Stat(config); err != nil {
		return "", "", &missingBridgeConfigError{dir: input}
	}
	return input, config, nil
}

// splitPinRef splits a `owner/repo@ref` shorthand into the repository and the
// pinned ref (commit SHA, tag or branch). A bare `owner/repo` has no ref.
func splitPinRef(s string) (repo, ref string) {
	if i := strings.LastIndexByte(s, '@'); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// validatePinRef enforces a strict git-ref grammar before a user-supplied ref
// is handed to rev-parse/checkout. A leading '-' would become a git option and
// revision expressions ("..", "~", "^", "@{") would escape the "one ref"
// contract, so they are rejected before any command runs.
func validatePinRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("invalid pinned ref: empty")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid pinned ref %q: leading '-' would be parsed as a git option", ref)
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") {
		return fmt.Errorf("invalid pinned ref %q: leading or trailing '/'", ref)
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return fmt.Errorf("invalid pinned ref %q: traversal or revision expression", ref)
	}
	if strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("invalid pinned ref %q: '.lock' suffix", ref)
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '+':
		default:
			return fmt.Errorf("invalid pinned ref %q: character %q is not allowed", ref, r)
		}
	}
	return nil
}

// checkoutPinnedRef checks that the pinned ref exists in the checkout and
// switches the cached bridge to it, so community-bridge resolution is pinned to
// a specific commit/tag rather than "whatever main is today".
func checkoutPinnedRef(dir, ref string) error {
	if err := validatePinRef(ref); err != nil {
		return err
	}
	if _, err := runGit(dir, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return fmt.Errorf("pinned ref %q not found in %s: %w", ref, dir, err)
	}
	_, err := runGit(dir, "checkout", "-q", ref)
	return err
}

// recordPin stores the pin in cache metadata so a later update cannot move the
// checkout off the requested ref. Legacy metadata without the field decodes to
// an empty pin and is left untouched.
func recordPin(entry RegistryEntry, ref string) error {
	meta, err := ensureMeta(entry)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = newMetaFromCache(entry)
	}
	meta.Pin = ref
	meta.Commit = gitHead(entry)
	meta.LastCheckedAt = time.Now()
	return WriteMeta(entry, meta)
}

// looksLikePath reports whether input is path-like — relative ("../x", "./x"),
// absolute ("/x", "\\x"), home-relative ("~/x") or a Windows drive
// ("C:\\x") — and therefore must be resolved only against the local
// filesystem, never cloned from a remote.
func looksLikePath(input string) bool {
	if input == "" {
		return false
	}
	switch input[0] {
	case '.', '/', '\\', '~':
		return true
	}
	if len(input) >= 2 && input[1] == ':' &&
		((input[0] >= 'a' && input[0] <= 'z') || (input[0] >= 'A' && input[0] <= 'Z')) {
		return true
	}
	return strings.ContainsRune(input, '\\')
}
