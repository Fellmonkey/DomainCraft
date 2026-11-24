// Package packages provides package registry resolution for bridge dependencies.
package packages

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var registryClient = &http.Client{Timeout: 5 * time.Second}

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
	var n int
	if _, err := fmt.Sscanf(major, "%d", &n); err != nil || n <= 0 {
		return 0
	}
	return n
}

func versionMajor(version string) int {
	var n int
	if _, err := fmt.Sscanf(version, "%d", &n); err != nil {
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
	url := strings.ReplaceAll(registryURL, "{id}", strings.ToLower(packageID))

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := registryClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
			fmt.Sscanf(aParts[i], "%d", &ai)
		}
		if i < len(bParts) {
			fmt.Sscanf(bParts[i], "%d", &bi)
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
