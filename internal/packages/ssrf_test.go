package packages

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

// S4: a hostname is not enough — its resolved addresses are checked at connect
// time, including link-local and unspecified addresses.
func TestRestrictedRegistryDialerRejectsInternalDNS(t *testing.T) {
	original := lookupRegistryIPs
	defer func() { lookupRegistryIPs = original }()

	tests := []struct {
		name    string
		ips     []net.IPAddr
		wantErr bool
	}{
		{name: "ipv4 loopback", ips: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, wantErr: true},
		{name: "ipv6 loopback", ips: []net.IPAddr{{IP: net.ParseIP("::1")}}, wantErr: true},
		{name: "private", ips: []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, wantErr: true},
		{name: "link local metadata", ips: []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, wantErr: true},
		{name: "unspecified", ips: []net.IPAddr{{IP: net.ParseIP("0.0.0.0")}}, wantErr: true},
		{name: "one bad address in a mixed answer", ips: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("10.0.0.1")}}, wantErr: true},
		{name: "no addresses", wantErr: true},
		{name: "public address", ips: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookupRegistryIPs = func(context.Context, string) ([]net.IPAddr, error) { return tt.ips, nil }
			u, err := url.Parse("https://registry.example/{id}")
			if err != nil {
				t.Fatal(err)
			}
			err = validateRegistryTarget(context.Background(), u)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRegistryTarget error = %v, wantErr %v", err, tt.wantErr)
			}

			// The dial hook must enforce the same rule before connecting. A
			// short context keeps the allowed case off the real network.
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, dialErr := restrictedRegistryDialer(ctx, "tcp", "registry.example:443")
			switch {
			case tt.wantErr && dialErr == nil:
				t.Fatal("restrictedRegistryDialer connected to a disallowed address")
			case !tt.wantErr && dialErr != nil &&
				(strings.Contains(dialErr.Error(), "not allowed") || strings.Contains(dialErr.Error(), "disallowed")):
				t.Fatalf("restrictedRegistryDialer rejected a public address: %v", dialErr)
			}
		})
	}
}

func TestRestrictedRegistryDialerRejectsLiteralAndRedirectTargets(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:443", "[::1]:443", "169.254.169.254:80", "0.0.0.0:443"} {
		t.Run(addr, func(t *testing.T) {
			_, err := restrictedRegistryDialer(context.Background(), "tcp", addr)
			if err == nil {
				t.Fatalf("restrictedRegistryDialer(%q) connected to a local address", addr)
			}
			if !strings.Contains(err.Error(), "not allowed") {
				t.Errorf("error = %q, want it to name the disallowed address", err)
			}
		})
	}

	// CheckRedirect validates the redirect target with the same DNS rule.
	original := lookupRegistryIPs
	defer func() { lookupRegistryIPs = original }()
	lookupRegistryIPs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}, nil
	}
	req, err := http.NewRequest(http.MethodGet, "https://redirect.example/index.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryClient.CheckRedirect(req, nil); err == nil {
		t.Error("CheckRedirect accepted a hostname resolving to a private address")
	}
}

func TestAllowLocalRegistryForTestsKeepsTLSServersUsable(t *testing.T) {
	original := lookupRegistryIPs
	defer func() { lookupRegistryIPs = original }()
	lookupRegistryIPs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	allowLocalRegistryForTests = true
	defer func() { allowLocalRegistryForTests = false }()

	u, err := url.Parse("https://localhost:8443/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRegistryTarget(context.Background(), u); err != nil {
		t.Fatalf("test seam rejected the local TLS server: %v", err)
	}
}

// S14: URL errors expose only scheme and host.
func TestValidateRegistryURLRedactsSecrets(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "plaintext", raw: "http://user:pass@api.example/v1?token=secret"},
		{name: "malformed", raw: "https://user:pass@[::1/v1?token=secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegistryURL(tt.raw)
			if err == nil {
				t.Fatalf("validateRegistryURL(%q) succeeded", tt.raw)
			}
			for _, secret := range []string{"pass", "secret", "user:"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error = %q, leaked %q", err, secret)
				}
			}
		})
	}
}

// S13: concurrent resolutions must not lose each other's cache entries, and a
// failed cache write must be reported to the caller.
func TestVersionCacheConcurrentWrites(t *testing.T) {
	resetCacheRoot(t)
	old := resolveVersionForFrameworkFn
	defer func() { resolveVersionForFrameworkFn = old }()
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) { return "1.0.0", nil }

	const count = 8
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := ResolveVersionCachedForFramework("shared", "https://reg.example/{id}", fmt.Sprintf("pkg%d", i), "net10.0")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ResolveVersionCachedForFramework: %v", err)
		}
	}
	entries, err := versionStore("shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("cached entries = %d, want %d", len(entries), count)
	}
}

func TestVersionCacheWriteFailureIsSurfaced(t *testing.T) {
	resetCacheRoot(t)
	oldResolve, oldPersist := resolveVersionForFrameworkFn, persistVersionCacheFn
	defer func() {
		resolveVersionForFrameworkFn, persistVersionCacheFn = oldResolve, oldPersist
	}()
	resolveVersionForFrameworkFn = func(_, _, _ string) (string, error) { return "2.0.0", nil }
	persistVersionCacheFn = func(string, map[string]versionEntry) error {
		return errors.New("disk full")
	}

	_, err := ResolveVersionCachedForFramework("demo", "https://reg.example/{id}", "demo.pkg", "net10.0")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("ResolveVersionCachedForFramework error = %v, want the write failure", err)
	}
}

// S9: registry requests are bound to the command context.
func TestFetchVersionsUsesCommandContext(t *testing.T) {
	srv := newTestRegistryServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"versions":["1.0.0"]}`))
	}))
	defer srv.Close()

	previous := bridge.CommandContext()
	defer bridge.SetCommandContext(previous)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.SetCommandContext(ctx)
	cancel()

	if _, err := ResolveVersionForFramework(srv.URL+"/{id}", "demo.pkg", ""); err == nil {
		t.Fatal("ResolveVersion ignored the cancelled command context")
	}
}

func TestRegistryClientIgnoresEnvironmentProxy(t *testing.T) {
	tr, ok := registryClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("registry transport = %T, want *http.Transport", registryClient.Transport)
	}
	if tr.Proxy != nil {
		t.Errorf("registry transport has a proxy function; dial-time SSRF checks would see the proxy host")
	}
}
