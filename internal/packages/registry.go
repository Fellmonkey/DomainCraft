// Package packages provides package registry resolution for bridge dependencies.
package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/netutil"
)

// maxRegistryResponseBytes caps a version index read from a registry.
const maxRegistryResponseBytes = 1 << 20

var registryClient = newRegistryClient()

// newRegistryClient installs a dialer that resolves and checks every address at
// connect time, including addresses reached through a redirect. Checking only
// the literal host in bridge.yaml is not enough: a hostname can resolve to
// loopback, a private network or the cloud metadata address.
func newRegistryClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = restrictedRegistryDialer
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if err := validateRegistryURL(req.URL.String()); err != nil {
				return err
			}
			return validateRegistryTarget(req.Context(), req.URL)
		},
	}
}

// allowLocalRegistryForTests is set only by tests using httptest TLS servers.
var allowLocalRegistryForTests bool

// lookupRegistryIPs is a seam so the SSRF guards can be tested without DNS.
var lookupRegistryIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// allowedRegistryIP rejects addresses that could reach a local service or the
// cloud metadata endpoint (loopback, private, link-local and unspecified).
func allowedRegistryIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

// validateRegistryURL performs the syntactic checks on a registry URL. The
// address checks happen at dial time so DNS rebinding cannot bypass them.
func validateRegistryURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Error repeats the raw URL (including userinfo/query); keep only
		// its reason and wrap that instead.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			err = urlErr.Err
		}
		return fmt.Errorf("invalid registry URL %q: %w", redactedRegistryURL(raw), err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("registry URL must be an absolute https URL: %q", redactedRegistryURL(raw))
	}
	host := strings.ToLower(u.Hostname())
	if allowLocalRegistryForTests {
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("registry host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil && !allowedRegistryIP(ip) {
		return fmt.Errorf("registry host %q is not allowed", host)
	}
	return nil
}

// validateRegistryTarget resolves the host and rejects a disallowed address.
func validateRegistryTarget(ctx context.Context, u *url.URL) error {
	if allowLocalRegistryForTests {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	ips, err := lookupRegistryIPs(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve registry host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("registry host %q has no addresses", host)
	}
	for _, addr := range ips {
		if !allowedRegistryIP(addr.IP) {
			return fmt.Errorf("registry host %q resolves to disallowed address %s", host, addr.IP)
		}
	}
	return nil
}

// restrictedRegistryDialer is the http.Transport dial hook. It uses the same
// address rule as validateRegistryTarget and connects to the checked IP, so a
// second DNS answer cannot rebind the connection to a private address.
func restrictedRegistryDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid registry address: %w", err)
	}
	dialer := &net.Dialer{}
	if allowLocalRegistryForTests {
		return dialer.DialContext(ctx, network, addr)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !allowedRegistryIP(ip) {
			return nil, fmt.Errorf("registry address %s is not allowed", redactedRegistryURL("https://"+addr))
		}
		return dialer.DialContext(ctx, network, addr)
	}
	ips, err := lookupRegistryIPs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve registry host: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("registry host %q has no addresses", host)
	}
	for _, candidate := range ips {
		if !allowedRegistryIP(candidate.IP) {
			return nil, fmt.Errorf("registry host %q resolves to disallowed address %s", host, candidate.IP)
		}
	}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// redactedRegistryURL keeps only scheme and host for error messages, so
// credentials and query strings from a bridge's config never reach the log.
// The redaction itself is shared with the other network callers.
func redactedRegistryURL(raw string) string {
	return netutil.RedactedURL(raw)
}

// ResolveVersion queries a package registry (e.g. NuGet) for the latest stable version
// of a package. The registryURL template must contain {id} which is replaced with the package ID.
func ResolveVersion(registryURL string, packageID string) (string, error) {
	versions, err := fetchVersions(registryURL, packageID)
	if err != nil {
		return "", err
	}
	best := latestStable(versions, 0)
	if best == "" {
		return "", fmt.Errorf("no stable versions found for %s", packageID)
	}
	return best, nil
}

// ResolveVersionForFramework resolves the latest stable version compatible with
// targetFramework (e.g. "net10.0"). NuGet packages for Microsoft/EF/Npgsql track
// the runtime major, so a version above the TFM major cannot be referenced by a
// project targeting that framework. Frameworks that are not "netN" leave the
// selection unconstrained. If no compatible version exists, the latest stable
// one is returned so generation can still complete; the renderer warns about it.
func ResolveVersionForFramework(registryURL, packageID, targetFramework string) (string, error) {
	versions, err := fetchVersions(registryURL, packageID)
	if err != nil {
		return "", err
	}
	maxMajor := MaxMajorForFramework(targetFramework)
	best := latestStable(versions, maxMajor)
	if best == "" {
		best = latestStable(versions, 0)
	}
	if best == "" {
		return "", fmt.Errorf("no stable versions found for %s", packageID)
	}
	return best, nil
}

// MaxMajorForFramework extracts the runtime major from "net10.0" → 10.
// It returns 0 for non-.NET or unparseable platforms (no version constraint).
func MaxMajorForFramework(framework string) int {
	if !strings.HasPrefix(framework, "net") {
		return 0
	}
	major := strings.TrimPrefix(framework, "net")
	if i := strings.IndexAny(major, ".-+ "); i >= 0 {
		major = major[:i]
	}
	n, err := strconv.Atoi(major)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func versionMajor(version string) int {
	i := 0
	for i < len(version) && version[i] >= '0' && version[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, err := strconv.Atoi(version[:i])
	if err != nil {
		return 0
	}
	return n
}

// latestStable returns the highest stable version whose major is <= maxMajor.
// maxMajor <= 0 means no constraint.
func latestStable(versions []string, maxMajor int) string {
	var best string
	for _, v := range versions {
		if strings.Contains(v, "-") {
			continue
		}
		if maxMajor > 0 && versionMajor(v) > maxMajor {
			continue
		}
		if best == "" || VersionGreater(v, best) {
			best = v
		}
	}
	return best
}

// fetchVersions reads the flat-container version index from the registry.
func fetchVersions(registryURL string, packageID string) ([]string, error) {
	registryEndpoint := strings.ReplaceAll(registryURL, "{id}", strings.ToLower(packageID))
	if err := validateRegistryURL(registryEndpoint); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(bridge.CommandContext(), http.MethodGet, registryEndpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := registryClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %d for %s", resp.StatusCode, packageID)
	}

	var result struct {
		Versions []string `json:"versions"`
	}
	// Limit response body to prevent OOM from malicious/misconfigured registries.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRegistryResponseBytes)).Decode(&result); err != nil {
		return nil, err
	}

	return result.Versions, nil
}

// VersionGreater compares two semver strings and returns true if a > b.
func VersionGreater(a, b string) bool {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	maxLen := max(len(aParts), len(bParts))
	for i := range maxLen {
		var ai, bi int
		if i < len(aParts) {
			ai, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bi, _ = strconv.Atoi(bParts[i])
		}
		if ai > bi {
			return true
		}
		if ai < bi {
			return false
		}
	}
	return false
}
