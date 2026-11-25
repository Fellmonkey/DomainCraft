package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/netutil"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
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
// implements. It aliases specmeta.ContractVersion so a bump needs one edit.
// An upstream bridge declaring a higher contract is unsupported and must never
// be applied.
const CoreContractVersion = specmeta.ContractVersion

// ParseContractVersion reads `contract_version` from bridge.yaml bytes. It is
// the single parser used by both the update gate and the renderer loader, so
// a manifest can never be accepted on one side and rejected on the other.
// Returns the effective version, whether the field was explicitly declared,
// and an error. An absent (or zero) field means the core's contract version;
// every other unreadable value — including a negative one — is a hard error:
// a malformed manifest must never be silently downgraded to "supported".
func ParseContractVersion(data []byte) (int, bool, error) {
	// Parse once into a node, then decode from it. A truncated body may still
	// be valid YAML (e.g. a scalar or a list), so require a mapping document
	// before reading the version.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0, false, fmt.Errorf("parse bridge contract version: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return 0, false, fmt.Errorf("parse bridge contract version: manifest must be a YAML mapping")
	}
	// A pointer distinguishes an absent field from an explicit 0, while the
	// declared flag follows the legacy "0 means absent" behaviour.
	var cfg struct {
		ContractVersion *int `yaml:"contract_version"`
	}
	if err := doc.Decode(&cfg); err != nil {
		return 0, false, fmt.Errorf("parse bridge contract version: %w", err)
	}
	if cfg.ContractVersion == nil {
		return CoreContractVersion, false, nil
	}
	if *cfg.ContractVersion < 0 {
		return 0, true, fmt.Errorf("invalid contract_version %d", *cfg.ContractVersion)
	}
	if *cfg.ContractVersion == 0 {
		return CoreContractVersion, false, nil
	}
	return *cfg.ContractVersion, true, nil
}

// parseBridgeContractVersion keeps the update-side seam used by probes and
// tests; the parsing itself lives in ParseContractVersion.
func parseBridgeContractVersion(data []byte) (int, error) {
	version, _, err := ParseContractVersion(data)
	return version, err
}

// CheckContractVersion verifies that a bridge declaring contractVersion is
// supported by the current core. A newer contract is a hard error — an
// unsupported bridge must never be downloaded/applied. Shared with the
// renderer so both gates move together on a contract bump.
func CheckContractVersion(contractVersion int) error {
	if contractVersion > CoreContractVersion {
		return fmt.Errorf("bridge requires contract v%d, but this core implements contract v%d", contractVersion, CoreContractVersion)
	}
	return nil
}

func shouldApplyContract(contractVersion int) error {
	return CheckContractVersion(contractVersion)
}

// remoteContractVersion fetches a bridge's declared contract version at a ref
// without cloning, via the GitHub raw-content endpoint. An unreadable manifest
// is an error: callers treat it as "cannot offer update" rather than guessing
// a supported version.
func remoteContractVersion(entry RegistryEntry, ref string) (int, error) {
	if ref == "" {
		ref = "main"
	}
	// The ref reaches a URL path before git ever validates it. Reject hostile
	// path/revision characters here so the contract gate cannot be pointed at a
	// different manifest than the ref ApplyUpdate later checks out.
	if err := validatePinRef(ref); err != nil {
		return 0, fmt.Errorf("invalid contract ref %q: %w", ref, err)
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

// validateFetchBranch applies the strict git-ref grammar used for pinned refs
// to the branch name passed to `git fetch`. The branch can come from cache
// metadata, so an attacker-controlled value must never become a git option or a
// revision expression.
func validateFetchBranch(branch string) error {
	if err := validatePinRef(branch); err != nil {
		return fmt.Errorf("invalid fetch branch %q: %w", branch, err)
	}
	return nil
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
	if err := validateFetchBranch(branch); err != nil {
		return "", err
	}

	// Fast path: update the existing checkout in place. Keep why it failed so a
	// fallback failure reports the original cause as well as the cleanup.
	var fastPathErrs []error
	if _, err := runGit(cacheDir, "fetch", "--depth", "1", "origin", branch); err != nil {
		fastPathErrs = append(fastPathErrs,
			fmt.Errorf("fetch branch %q of bridge %q in %q: %w", branch, entry.ID, cacheDir, err))
	} else {
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
		if _, err := runGit(cacheDir, "reset", "--hard", "FETCH_HEAD"); err != nil {
			fastPathErrs = append(fastPathErrs,
				fmt.Errorf("reset bridge %q in %q to FETCH_HEAD: %w", entry.ID, cacheDir, err))
		} else {
			return finishUpdate(entry, branch, pin)
		}
		// reset failed — fall through to a clean re-clone
	}

	// Fallback: the existing checkout is unusable; replace it entirely. Report
	// the in-place failure (primary) alongside any cleanup/re-clone failure.
	if err := os.RemoveAll(cacheDir); err != nil {
		return "", errors.Join(append(fastPathErrs,
			fmt.Errorf("remove cache dir %q: %w", cacheDir, err))...)
	}
	if err := CloneBridge(entry); err != nil {
		return "", errors.Join(append(fastPathErrs,
			fmt.Errorf("re-clone bridge %q into %q: %w", entry.ID, cacheDir, err))...)
	}
	// The fallback clone is a fresh checkout of the default branch, so it must
	// pass the same contract gate as the fast path. Fail closed: an unsupported
	// bridge is never recorded as the new cache, and the in-place failure stays
	// as the primary cause alongside the contract and cleanup errors.
	if cerr := checkCachedContract(entry); cerr != nil {
		errs := append(append([]error{}, fastPathErrs...), cerr)
		if rerr := os.RemoveAll(cacheDir); rerr != nil {
			errs = append(errs, fmt.Errorf("remove unsupported bridge cache %q: %w", cacheDir, rerr))
		}
		return "", errors.Join(errs...)
	}
	return finishUpdate(entry, branch, pin)
}

// checkCachedContract reads the freshly cloned bridge.yaml and rejects a
// contract this core cannot support. An unreadable manifest is an error, never
// a pass — the same rule the fast path applies to FETCH_HEAD.
func checkCachedContract(entry RegistryEntry) error {
	data, err := os.ReadFile(filepath.Join(CachePath(entry), "bridge.yaml"))
	if err != nil {
		return fmt.Errorf("read re-cloned bridge manifest: %w", err)
	}
	version, _, err := ParseContractVersion(data)
	if err != nil {
		return err
	}
	return CheckContractVersion(version)
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
	// The API response is remote data: validate it before it becomes a URL path
	// or, later, the branch passed to git fetch.
	if err := validatePinRef(repo.DefaultBranch); err != nil {
		return "", "", fmt.Errorf("github API: %w", err)
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

// validateProbeRedirect is the redirect rule for bridge/update HTTP probes.
// netutil installs it as CheckRedirect, so it runs before each follow-up
// request is sent — checking resp.Request.URL after client.Do is too late.
// Only the HTTPS hosts GitHub uses for these probes are allowed.
func validateProbeRedirect(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("redirect without a target URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("redirect scheme %q is not allowed", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "github.com", "api.github.com", "raw.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("host %q is not allowed", host)
	}
}

// httpGetBody performs a GET bound to the CLI's command context and returns the
// response body. The bounded GET, status check and redaction live in netutil;
// this wrapper keeps the package's test seam and the 10s probe client, and
// passes a redirect rule so every hop is validated before the request to the
// new host is made.
func httpGetBody(rawURL string) ([]byte, error) {
	return netutil.BoundedGet(CommandContext(), httpClient, rawURL, int64(maxProbeBodyBytes), validateProbeRedirect)
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
