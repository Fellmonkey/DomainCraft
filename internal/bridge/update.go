package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// httpClient is shared by remote version probes.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// httpGetBodyFn is the HTTP GET used by update probes. It is a variable so
// tests can substitute a fake and exercise the GitHub code paths offline.
var httpGetBodyFn = httpGetBody

// DefaultUpdateInterval is how often cached bridges are polled against their
// remote for newer versions. It mirrors the TTL used by common package
// managers: fresh enough that template fixes reach users, sparse enough that
// an invocation does not hit the network every time.
const DefaultUpdateInterval = 24 * time.Hour

// CoreContractVersion is the bridge-contract version the current core
// implements. It must match renderer.ContractVersion. An upstream bridge
// declaring a higher contract is unsupported and must never be applied.
const CoreContractVersion = 1

// parseBridgeContractVersion reads `contract_version` from bridge.yaml bytes.
// An absent field means contract v1. Any other unreadable value is an error:
// a malformed manifest must never be silently downgraded to "supported".
func parseBridgeContractVersion(data []byte) (int, error) {
	// A truncated body may still be valid YAML (e.g. a scalar or a list), so
	// require the document to be a mapping before reading the version.
	var probe any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return 0, fmt.Errorf("parse bridge contract version: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return 0, fmt.Errorf("parse bridge contract version: manifest must be a YAML mapping")
	}
	var cfg struct {
		ContractVersion int `yaml:"contract_version"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("parse bridge contract version: %w", err)
	}
	switch {
	case cfg.ContractVersion == 0:
		return 1, nil
	case cfg.ContractVersion < 0:
		return 0, fmt.Errorf("invalid contract_version %d", cfg.ContractVersion)
	default:
		return cfg.ContractVersion, nil
	}
}

// shouldApplyContract verifies that a bridge declaring contractVersion is
// supported by the current core. A newer contract is a hard error — an
// unsupported bridge must never be downloaded/applied.
func shouldApplyContract(contractVersion int) error {
	if contractVersion > CoreContractVersion {
		return fmt.Errorf("bridge requires contract v%d, but this core implements contract v%d", contractVersion, CoreContractVersion)
	}
	return nil
}

// remoteContractVersion fetches a bridge's declared contract version at a ref
// without cloning, via the GitHub raw-content endpoint. An unreadable manifest
// is an error: callers treat it as "cannot offer update" rather than guessing
// a supported version.
func remoteContractVersion(entry RegistryEntry, ref string) (int, error) {
	if ref == "" {
		ref = "main"
	}
	body, err := httpGetBodyFn(fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/bridge.yaml", entry.GitHub, ref))
	if err != nil {
		return 0, err
	}
	return parseBridgeContractVersion(body)
}

// Update describes a detected newer version of a cached bridge. Callers hand it
// to the user (e.g. an interactive prompt) and then to ApplyUpdate.
type Update struct {
	Entry        RegistryEntry
	LocalVersion string // version of the cached checkout
	RemoteCommit string
	RemoteBranch string
}

// CheckForUpdate detects whether a cached bridge has a newer version upstream.
//
// The remote is only contacted when the cached copy was last checked more than
// interval ago (interval <= 0 always checks). The check is best-effort: an
// unreachable remote is treated as "up to date" rather than as an error, so
// offline generation keeps working with the cached copy. A nil result means the
// bridge is current (or the check was skipped).
func CheckForUpdate(entry RegistryEntry, interval time.Duration) (*Update, error) {
	meta, err := ensureMeta(entry)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil // not cached
	}

	now := time.Now()
	if interval > 0 && now.Sub(meta.LastCheckedAt) < interval {
		return nil, nil // checked recently enough — don't hit the network
	}

	// Keep the local commit in sync with the actual checkout so a manual pull
	// of the cache does not incorrectly report an update.
	if head := gitHead(entry); head != "" {
		meta.Commit = head
	}

	remoteBranch, remoteCommit, err := githubAPIHead(entry)

	// Record the attempt regardless of outcome so the freshness gate applies
	// even while offline (otherwise every run would wait on a timeout).
	meta.LastCheckedAt = now
	if werr := WriteMeta(entry, meta); werr != nil && err == nil {
		return nil, werr
	}

	if err != nil || remoteCommit == "" {
		return nil, nil // can't reach the remote — assume up to date
	}
	if remoteCommit == meta.Commit {
		return nil, nil // local checkout already at the remote tip
	}

	return &Update{
		Entry:        entry,
		LocalVersion: meta.Version,
		RemoteCommit: remoteCommit,
		RemoteBranch: remoteBranch,
	}, nil
}

// ApplyUpdate brings a cached bridge up to the upstream default branch,
// preserving the checkout via a shallow fetch + hard reset (a full re-clone is
// used as a fallback if the in-place update fails). It returns the new version.
func ApplyUpdate(entry RegistryEntry, u *Update) (string, error) {
	if err := validateCacheID(entry.ID); err != nil {
		return "", err
	}
	cacheDir := CachePath(entry)

	// A pinned checkout has an explicit ref. Remember it so a direct ApplyUpdate
	// never leaves the cache on the default branch.
	pin := ""
	if meta, err := ReadMeta(entry); err == nil && meta != nil {
		pin = meta.Pin
	}

	branch := "main"
	if u != nil && u.RemoteBranch != "" {
		branch = u.RemoteBranch
	} else if meta, err := ReadMeta(entry); err == nil && meta != nil && meta.Branch != "" {
		branch = meta.Branch
	}

	// Fast path: update the existing checkout in place.
	if _, err := runGit(cacheDir, "fetch", "--depth", "1", "origin", branch); err == nil {
		// Contract gate: never apply an upstream bridge this core cannot support.
		// The manifest must be readable — fail closed when it is not.
		out, serr := runGit(cacheDir, "show", "FETCH_HEAD:bridge.yaml")
		if serr != nil {
			return "", fmt.Errorf("read fetched bridge manifest: %w", serr)
		}
		contract, perr := parseBridgeContractVersion([]byte(out))
		if perr != nil {
			return "", perr
		}
		if cerr := shouldApplyContract(contract); cerr != nil {
			return "", cerr
		}
		if _, err := runGit(cacheDir, "reset", "--hard", "FETCH_HEAD"); err == nil {
			return finishUpdate(entry, branch, pin)
		}
		// reset failed — fall through to a clean re-clone
	}

	// Fallback: the existing checkout is unusable; replace it entirely.
	if err := os.RemoveAll(cacheDir); err != nil {
		return "", err
	}
	if err := CloneBridge(entry); err != nil {
		return "", err
	}
	return finishUpdate(entry, branch, pin)
}

// finishUpdate re-applies a pin (if any) before recording metadata, so an
// updated cache never drifts from the ref the user asked for.
func finishUpdate(entry RegistryEntry, branch, pin string) (string, error) {
	if pin != "" {
		if err := checkoutPinnedRef(CachePath(entry), pin); err != nil {
			return "", fmt.Errorf("restore pinned ref %q after update: %w", pin, err)
		}
	}
	version, err := syncMetaAfterUpdate(entry, branch)
	if err != nil || pin == "" {
		return version, err
	}
	meta, err := ReadMeta(entry)
	if err != nil {
		return version, err
	}
	if meta == nil {
		meta = &CacheMeta{ID: entry.ID, Source: entry.GitHub}
	}
	meta.Pin = pin
	meta.Commit = gitHead(entry)
	if err := WriteMeta(entry, meta); err != nil {
		return version, err
	}
	return version, nil
}

// syncMetaAfterUpdate refreshes the cached checkout's metadata after a
// successful download/update and returns the new bridge version.
func syncMetaAfterUpdate(entry RegistryEntry, branch string) (string, error) {
	meta := newMetaFromCache(entry)
	if branch != "" {
		meta.Branch = branch
	}
	if err := WriteMeta(entry, meta); err != nil {
		return meta.Version, err
	}
	return meta.Version, nil
}

// githubAPIHead resolves the upstream default branch and its head commit
// through the GitHub REST API. Two requests (repo metadata, then branch
// commit) that do not touch the local checkout at all.
func githubAPIHead(entry RegistryEntry) (branch, commit string, err error) {
	repoBody, err := httpGetBodyFn(fmt.Sprintf("https://api.github.com/repos/%s", entry.GitHub))
	if err != nil {
		return "", "", err
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(repoBody, &repo); err != nil {
		return "", "", err
	}
	if repo.DefaultBranch == "" {
		return "", "", fmt.Errorf("github API: no default branch for %s", entry.GitHub)
	}

	commitBody, err := httpGetBodyFn(fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", entry.GitHub, repo.DefaultBranch))
	if err != nil {
		return "", "", err
	}
	var c struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(commitBody, &c); err != nil {
		return "", "", err
	}
	if c.SHA == "" {
		return "", "", fmt.Errorf("github API: no head commit for %s/%s", entry.GitHub, repo.DefaultBranch)
	}
	return repo.DefaultBranch, c.SHA, nil
}

// maxProbeBodyBytes caps remote probe responses (bridge.yaml, GitHub JSON).
const maxProbeBodyBytes = 1 << 20

// httpGetBody performs a GET bound to the CLI's command context and returns the
// response body. It reads one byte past the limit so an oversized body fails
// instead of being silently truncated into a plausible-looking manifest.
func httpGetBody(rawURL string) ([]byte, error) {
	scope := redactedURLScope(rawURL)
	req, err := http.NewRequestWithContext(CommandContext(), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build probe request for %s: %w", scope, redactURLError(err))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe request to %s failed: %w", scope, redactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d for %s", resp.StatusCode, scope)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", scope, err)
	}
	if len(body) > maxProbeBodyBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", scope, maxProbeBodyBytes)
	}
	return body, nil
}

// redactURLError drops the raw URL carried by *url.Error (userinfo and query
// could be secrets) while keeping the underlying reason for %w wrapping.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

// redactedURLScope reduces a URL to scheme and host for error messages, so
// credentials and query strings from a bridge's config never reach the log.
func redactedURLScope(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return "<invalid url>"
	}
	return u.Scheme + "://" + u.Hostname()
}

// runGit runs a git command in dir and returns its trimmed stdout.
//
// The child inherits the environment, but git-location variables (GIT_DIR,
// GIT_WORK_TREE, ...) are scrubbed so a developer who exports them globally
// can never redirect a bridge-cache operation onto the wrong repository — the
// command always acts on the repo in dir.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(commandCtx, "git", args...) //nolint:gosec // args passed separately; no shell interpretation
	cmd.Dir = dir
	cmd.Env = gitScrubbedEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitEnvironmentNames is an allowlist for git children. Git reads a large
// family of GIT_CONFIG_*/GIT_SSH_COMMAND/proxy variables that can redirect a
// cache operation onto another repository or another transport; none of them
// are needed to fetch a bridge. Only process basics survive.
var gitEnvironmentNames = map[string]bool{
	"HOME": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "COMSPEC": true,
	"TEMP": true, "TMP": true, "TMPDIR": true, "APPDATA": true,
	"LOCALAPPDATA": true, "PROGRAMDATA": true, "LANG": true, "LC_ALL": true,
	"LANGUAGE": true, "TZ": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
}

// gitScrubbedEnv returns a minimal environment for git children. It keeps the
// variables git needs to find its binary, HOME and the temp dir on Windows/CI,
// and drops everything else (notably all GIT_* overrides and proxies).
func gitScrubbedEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if gitEnvironmentNames[strings.ToUpper(name)] {
			env = append(env, kv)
		}
	}
	return env
}
