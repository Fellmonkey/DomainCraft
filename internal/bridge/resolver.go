package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if info, err := os.Stat(input); err == nil {
		if info.IsDir() {
			bridgeFile := filepath.Join(input, "bridge.yaml")
			if _, err := os.Stat(bridgeFile); err == nil {
				return input, nil
			}
			return "", fmt.Errorf("directory %q does not contain bridge.yaml", input)
		}
		return filepath.Dir(input), nil
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
		entry := RegistryEntry{
			ID:     parts[1],
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
		}
		return path, nil
	}

	return "", fmt.Errorf("bridge %q not found: not a local path, registry ID, or owner/repo", input)
}

// splitPinRef splits a `owner/repo@ref` shorthand into the repository and the
// pinned ref (commit SHA, tag or branch). A bare `owner/repo` has no ref.
func splitPinRef(s string) (repo, ref string) {
	if i := strings.LastIndexByte(s, '@'); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// checkoutPinnedRef checks that the pinned ref exists in the checkout and
// switches the cached bridge to it, so community-bridge resolution is pinned to
// a specific commit/tag rather than "whatever main is today".
func checkoutPinnedRef(dir, ref string) error {
	if _, err := runGit(dir, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return fmt.Errorf("pinned ref %q not found in %s: %w", ref, dir, err)
	}
	_, err := runGit(dir, "checkout", "-q", ref)
	return err
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
