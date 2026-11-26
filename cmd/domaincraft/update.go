package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/appdir"
	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/netutil"
	"github.com/DomainCraft/DomainCraft/internal/packages"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
)

// coreRepo is the GitHub owner/repo that publishes domaincraft releases.
const coreRepo = "DomainCraft/DomainCraft"

const releasesLatestURL = "https://api.github.com/repos/DomainCraft/DomainCraft/releases/latest"

// checksumsAssetName is the release asset holding one SHA-256 digest per binary.
const checksumsAssetName = "checksums.txt"

// releaseHosts is the strict allowlist for release URLs. A release payload is
// remote data: only these hosts may supply a binary that replaces this process.
var releaseHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// httpClient is the HTTP client used by release checks and binary downloads.
var httpClient = &http.Client{Timeout: 120 * time.Second}

// noticeClient bounds the automatic "new version available" probe. It shares
// the release transport and redirect policy but must never make an unrelated
// command wait for a download-sized timeout.
var noticeClient = &http.Client{
	Timeout:       2 * time.Second,
	Transport:     httpClient.Transport,
	CheckRedirect: httpClient.CheckRedirect,
}

// githubRelease is the subset of the GitHub releases/latest payload we need.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// updateNoticeTTL throttles the automatic "new version available" notice so it
// does not hit the GitHub API on every CLI invocation.
const updateNoticeTTL = 24 * time.Hour

// updateCheckCache is the on-disk record of the last core update check.
type updateCheckCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// releaseVersion strips the optional v prefix from the stamped CLI version.
func releaseVersion() string { return strings.TrimPrefix(version, "v") }

// updateAvailableMessage is the single wording for "a newer core exists",
// shared by the passive notice and `update --check`.
func updateAvailableMessage(latest, current string) string {
	return fmt.Sprintf("A new domaincraft version is available: v%s (current v%s). Run `domaincraft update` to install it.", latest, current)
}

// maybeNotifyUpdate prints a one-line notice to the command's error stream when
// a newer core release exists. It is best-effort, throttled by a local cache,
// and skipped for development builds and for the `update` command itself.
func maybeNotifyUpdate(cmd *cobra.Command) { maybeNotifyUpdateWithOps(defaultUpdateOps(), cmd) }

func maybeNotifyUpdateWithOps(ops updateOps, cmd *cobra.Command) {
	current := releaseVersion()
	if current == "" || current == "dev" || cmd.Name() == "update" {
		return
	}
	latest, err := cachedLatestCoreVersionWithOps(ops)
	if err != nil || latest == "" {
		return
	}
	if packages.VersionGreater(latest, current) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ℹ %s\n", updateAvailableMessage(latest, current))
	}
}

// cachedLatestCoreVersion returns the latest released core version, using the
// throttled cache when it is fresh and otherwise querying GitHub.
func cachedLatestCoreVersion() (string, error) { return cachedLatestCoreVersionWithOps(defaultUpdateOps()) }

func cachedLatestCoreVersionWithOps(ops updateOps) (string, error) {
	cacheDir := appdir.CacheDir("core")
	cachePath := filepath.Join(cacheDir, "core-update.json")
	if data, rerr := os.ReadFile(cachePath); rerr == nil { //nolint:gosec // cachePath is built by appdir helpers
		var c updateCheckCache
		if json.Unmarshal(data, &c) == nil && !c.CheckedAt.IsZero() && time.Since(c.CheckedAt) < updateNoticeTTL {
			return c.Latest, nil
		}
	}

	rel, err := ops.latestForNotice()
	if err != nil {
		// Record the attempt as well: an offline or blackholed network must not
		// make every later invocation wait for another HTTP timeout again.
		if werr := writeUpdateCheckCache(cacheDir, cachePath, updateCheckCache{CheckedAt: time.Now()}); werr != nil {
			return "", errors.Join(err, fmt.Errorf("cache update check: %w", werr))
		}
		return "", err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if err := writeUpdateCheckCache(cacheDir, cachePath, updateCheckCache{CheckedAt: time.Now(), Latest: latest}); err != nil {
		return "", fmt.Errorf("cache update check: %w", err)
	}
	return latest, nil
}

func writeUpdateCheckCache(cacheDir, cachePath string, c updateCheckCache) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil { //nolint:gosec // cache dirs must stay readable across CLI runs
		return err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(cachePath, body)
}

// newUpdateCmd checks for and installs a newer version of the domaincraft core.
func newUpdateCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the domaincraft core to the latest release",
		Long:  "Check the GitHub releases for a newer domaincraft version and, if available, download and replace the current executable.\nUse --check to only report whether a newer version exists, without downloading.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoreUpdate(commandLogger(cmd), checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer version exists (no download)")
	return cmd
}

// runCoreUpdate implements `domaincraft update` (and `domaincraft update --check`).
func runCoreUpdate(log *logger.Logger, checkOnly bool) error {
	return runCoreUpdateWithOps(defaultUpdateOps(), log, checkOnly)
}

func runCoreUpdateWithOps(ops updateOps, log *logger.Logger, checkOnly bool) error {
	current := releaseVersion()
	if current == "" || current == "dev" {
		log.Info("Running a development build (v%s).", version)
		if !checkOnly {
			log.Info("To update, run: go install github.com/DomainCraft/DomainCraft/cmd/domaincraft@latest")
		}
		return nil
	}

	log.Info("Checking for updates (current: v%s)...", current)
	release, err := latestReleaseWithOps(ops)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	latest := strings.TrimPrefix(release.TagName, "v")

	if !packages.VersionGreater(latest, current) {
		log.Success("Already up to date (v%s).", current)
		return nil
	}

	if checkOnly {
		log.Info("%s", updateAvailableMessage(latest, current))
		return nil
	}

	// Fail closed: validate the remote URL and the checksum before any bytes
	// are written, and again against the downloaded bytes before replacing the
	// running executable.
	asset := assetName()
	assetURL, err := validatedReleaseAssetURL(release, asset)
	if err != nil {
		return err
	}
	wantSum, err := releaseChecksumWithOps(ops, release, asset)
	if err != nil {
		return err
	}
	// Resolve the executable before downloading: the temp file must live on the
	// same volume as the binary, otherwise rename fails with EXDEV on install.
	exe, err := ops.executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	tmp, err := downloadBinaryWithOps(ops, assetURL, filepath.Dir(exe))
	if err != nil {
		return err
	}
	defer func() {
		if cerr := ops.remove(tmp); cerr != nil && !errors.Is(cerr, os.ErrNotExist) {
			log.Warn("could not remove temporary update %s: %v", tmp, cerr)
		}
	}()

	if err := verifyFileSHA256(tmp, wantSum); err != nil {
		return fmt.Errorf("refusing to install update: %w", err)
	}

	if err := ops.replaceExecutable(tmp, exe); err != nil {
		return err
	}
	// The new binary is in place only now: a failed rename above never reports
	// success, even when the old executable had to be moved aside first.
	log.Success("Updated to v%s (was v%s). Re-run the command to use the new version.", latest, current)
	return nil
}

// decodeRelease validates the release payload shared by the explicit update
// command and the automatic notice probe.
func decodeRelease(body []byte) (*githubRelease, error) {
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no releases found for %s", coreRepo)
	}
	return &rel, nil
}

// latestRelease fetches the newest published release metadata.
func latestRelease() (*githubRelease, error) { return latestReleaseWithOps(defaultUpdateOps()) }

func latestReleaseWithOps(ops updateOps) (*githubRelease, error) {
	body, err := ops.getBody(releasesLatestURL)
	if err != nil {
		return nil, err
	}
	return decodeRelease(body)
}

// assetName returns the upload name of the binary for the current platform,
// matching the naming used by the release pipeline: domaincraft-<os>-<arch>,
// with a .exe suffix on Windows.
func assetName() string {
	name := fmt.Sprintf("domaincraft-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// releaseAssetFor finds the download URL of a release asset matching name.
func releaseAssetFor(rel *githubRelease, name string) (string, error) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no prebuilt binary %s for this release (platform %s/%s); use `go install` instead", name, runtime.GOOS, runtime.GOARCH)
}

// validatedReleaseAssetURL finds a release asset and validates its URL before
// the bytes are used. Only https and known GitHub hosts are accepted.
func validatedReleaseAssetURL(rel *githubRelease, name string) (string, error) {
	raw, err := releaseAssetFor(rel, name)
	if err != nil {
		return "", err
	}
	u, err := validateReleaseURL(raw)
	if err != nil {
		return "", fmt.Errorf("refusing download for %s: %w", name, err)
	}
	return u, nil
}

// validateReleaseURL accepts only https URLs on the release host allowlist,
// without credentials, query or fragment.
func validateReleaseURL(raw string) (string, error) {
	u, err := parseAllowedURL(raw)
	if err != nil {
		return "", err
	}
	if u.User != nil {
		return "", fmt.Errorf("refusing release URL with credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("refusing release URL with query or fragment")
	}
	return u.String(), nil
}

// parseAllowedURL checks scheme and host only. Redirects from GitHub carry
// signed query strings, so those are allowed here but never in a URL taken
// from release JSON.
func parseAllowedURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid release URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("refusing non-https release URL")
	}
	if !releaseHosts[strings.ToLower(u.Hostname())] {
		return nil, fmt.Errorf("refusing release URL on host %q", u.Hostname())
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, fmt.Errorf("refusing release URL on port %q", p)
	}
	return u, nil
}

// releaseChecksum reads the SHA-256 digest for name from the release's
// checksums.txt asset. A missing, malformed or foreign digest is an error.
func releaseChecksum(rel *githubRelease, name string) (string, error) {
	return releaseChecksumWithOps(defaultUpdateOps(), rel, name)
}

func releaseChecksumWithOps(ops updateOps, rel *githubRelease, name string) (string, error) {
	assetURL, err := validatedReleaseAssetURL(rel, checksumsAssetName)
	if err != nil {
		return "", fmt.Errorf("verify update: %w", err)
	}
	data, err := ops.getBody(assetURL)
	if err != nil {
		return "", fmt.Errorf("fetch release checksums: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		sum := strings.ToLower(fields[0])
		if _, err := hex.DecodeString(sum); err != nil || len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid sha-256 checksum for %s", name)
		}
		return sum, nil
	}
	return "", fmt.Errorf("no sha-256 checksum for %s in %s", name, checksumsAssetName)
}

// verifyFileSHA256 compares a downloaded file's digest against want.
func verifyFileSHA256(path, want string) error {
	f, err := os.Open(path) //nolint:gosec // path is the verified temporary release asset
	if err != nil {
		return fmt.Errorf("open downloaded update: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash downloaded update: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for downloaded update (got %s, want %s)", got, want)
	}
	return nil
}

// validateReleaseRedirect keeps a redirect from moving the download off the
// release-host allowlist. Signed query strings on GitHub's asset redirects are
// allowed here; validateReleaseURL still rejects them on URLs from release JSON.
func validateReleaseRedirect(u *url.URL) error {
	_, err := parseAllowedURL(u.String())
	return err
}

// httpGetBody performs a GET bound to bridge.CommandContext() and returns the response body,
// capping it at the configured maxBodyBytes. The shared helper owns the status check,
// cap and redaction; this package supplies the client, context and redirect
// policy.
func httpGetBody(rawURL string) ([]byte, error) { return httpGetBodyWithOps(defaultUpdateOps(), rawURL) }

func httpGetBodyWithOps(ops updateOps, rawURL string) ([]byte, error) {
	return netutil.BoundedGet(bridge.CommandContext(), httpClient, rawURL, ops.maxBodyBytes, validateReleaseRedirect)
}

// downloadBinary fetches a release asset into a temp file in dir and returns
// its path. Callers pass filepath.Dir(exe) so the final rename never crosses a
// volume boundary (EXDEV on Unix, a needless copy on Windows).
func downloadBinary(rawURL, dir string) (string, error) {
	return downloadBinaryWithOps(defaultUpdateOps(), rawURL, dir)
}

func downloadBinaryWithOps(ops updateOps, rawURL, dir string) (string, error) {
	validURL, err := validateReleaseURL(rawURL)
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}
	data, err := ops.getBody(validURL)
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}

	tmp, err := ops.createTemp(dir, "domaincraft-update-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = tmp.Close() }()

	if _, err := tmp.Write(data); err != nil {
		return "", errors.Join(err, ops.remove(tmp.Name()))
	}
	if runtime.GOOS != "windows" {
		// A non-executable download must never reach the install step.
		if err := ops.chmod(tmp.Name(), 0o755); err != nil {
			return "", errors.Join(fmt.Errorf("make downloaded update executable: %w", err), ops.remove(tmp.Name()))
		}
	}
	return tmp.Name(), nil
}

// replaceExecutable puts the new binary in place of the running executable. It
// first tries a direct rename; on platforms where the running binary is locked
// (Windows), it moves the current one aside first.
func replaceExecutable(tmp, exe string) error {
	return replaceExecutableWithOps(defaultUpdateOps(), tmp, exe)
}

func replaceExecutableWithOps(ops updateOps, tmp, exe string) error {
	// Direct rename is the normal path. Its error is kept: on a locked binary
	// it explains why the fallback was needed, and on a real failure it is the
	// only cause reported to the user.
	firstErr := ops.rename(tmp, exe)
	if firstErr == nil {
		return nil
	}

	// Windows keeps the running executable locked, so move it aside first.
	// Every failure mentions `old` — a human must be able to recover by hand.
	old := exe + ".old"
	if err := ops.remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove previous backup %s: %w", old, err)
	}
	if err := ops.rename(exe, old); err != nil {
		return fmt.Errorf("update in place failed; replace %s manually or run go install (backup path %s): %w",
			exe, old, errors.Join(firstErr, fmt.Errorf("move %s to %s: %w", exe, old, err)))
	}
	if err := ops.rename(tmp, exe); err != nil {
		restoreErr := ops.rename(old, exe)
		return fmt.Errorf("could not install update to %s (backup at %s): %w", exe, old, errors.Join(
			fmt.Errorf("initial rename: %w", firstErr),
			fmt.Errorf("rename update into place: %w", err),
			fmt.Errorf("restore %s from %s: %w", exe, old, restoreErr),
		))
	}
	if err := ops.remove(old); err != nil {
		return fmt.Errorf("update installed to %s but could not remove backup %s: %w", exe, old, err)
	}
	return nil
}
