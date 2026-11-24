// Package packages provides package registry resolution for bridge dependencies.
package packages

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var registryClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if err := validateRegistryURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	},
}

// allowLocalRegistryForTests is set only by tests using httptest TLS servers.
var allowLocalRegistryForTests bool

// validateRegistryURL keeps registry requests on https and away from loopback,
// private and link-local addresses. registry_url comes from bridge.yaml, which
// may be cloned from an untrusted repository.
func validateRegistryURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid registry URL %q: %w", raw, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("registry URL must be an absolute https URL: %q", raw)
	}
	host := strings.ToLower(u.Hostname())
	if allowLocalRegistryForTests {
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("registry host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil &&
		(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("registry host %q is not allowed", host)
	}
	return nil
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

	req, err := http.NewRequest("GET", registryEndpoint, nil)
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
	// Limit response body to 1MB to prevent OOM from malicious/misconfigured registries.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}

	return result.Versions, nil
}

// VersionGreater compares two semver strings and returns true if a > b.
func VersionGreater(a, b string) bool {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}
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
