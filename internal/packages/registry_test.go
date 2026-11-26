package packages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestRegistryServer switches the registry client to the TLS test server so
// the https-only guard can stay enabled in production.
func newTestRegistryServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	previous := registryClient
	registryClient = srv.Client()
	allowLocalRegistryForTests = true
	t.Cleanup(func() {
		registryClient = previous
		allowLocalRegistryForTests = false
		srv.Close()
	})
	return srv
}

func TestValidateRegistryURLRejectsInternalTargets(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "public https", url: "https://api.nuget.org/v3-flatcontainer/{id}/index.json"},
		{name: "plaintext", url: "http://api.nuget.org/v3/{id}", wantErr: true},
		{name: "loopback ip", url: "https://127.0.0.1:8080/admin/{id}", wantErr: true},
		{name: "localhost", url: "https://localhost/{id}", wantErr: true},
		{name: "link local", url: "https://169.254.169.254/latest/{id}", wantErr: true},
		{name: "private ip", url: "https://10.0.0.5/{id}", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegistryURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRegistryURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestVersionGreater(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"major greater", "2.0.0", "1.0.0", true},
		{"major lesser", "1.0.0", "2.0.0", false},
		{"minor greater", "1.2.0", "1.1.0", true},
		{"minor lesser", "1.1.0", "1.2.0", false},
		{"patch greater", "1.0.2", "1.0.1", true},
		{"patch lesser", "1.0.1", "1.0.2", false},
		{"equal", "1.0.0", "1.0.0", false},
		{"different lengths", "1.0", "1.0.0", false},
		{"different lengths reverse", "1.0.0", "1.0", false},
		{"three-part major", "10.0.0", "9.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VersionGreater(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("VersionGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestResolveVersionPicksHighestStable(t *testing.T) {
	srv := newTestRegistryServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/newtonsoft.json/index.json" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"versions":["1.0.0","9.0.1","9.0.2-beta1","10.0.0-rc.2","9.0.2","2.0.0"]}`))
	}))

	got, err := ResolveVersionForFramework(srv.URL+"/{id}/index.json", "Newtonsoft.Json", "")
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}
	if got != "9.0.2" {
		t.Errorf("version = %q, want 9.0.2 (highest stable; pre-releases skipped)", got)
	}
}

func TestMaxMajorForFramework(t *testing.T) {
	cases := map[string]int{
		"net10.0": 10, "net11.0": 11, "net9.0": 9,
		"netstandard2.0": 0, "": 0, "java21": 0,
	}
	for framework, want := range cases {
		if got := MaxMajorForFramework(framework); got != want {
			t.Errorf("MaxMajorForFramework(%q) = %d, want %d", framework, got, want)
		}
	}
}

// Regression: the NuGet resolver used to return the newest stable version even
// when the generated csproj targets an older runtime (efcore 11.x vs net10.0).
func TestResolveVersionForFrameworkRespectsTargetFramework(t *testing.T) {
	srv := newTestRegistryServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"versions":["9.0.9","11.0.0","10.0.0","12.0.0-rc1"]}`))
	}))

	for _, tc := range []struct {
		name      string
		framework string
		want      string
	}{
		{name: "net10 target", framework: "net10.0", want: "10.0.0"},
		{name: "net11 target", framework: "net11.0", want: "11.0.0"},
		{name: "netstandard", framework: "netstandard2.0", want: "11.0.0"},
		{name: "empty framework", framework: "", want: "11.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveVersionForFramework(srv.URL+"/{id}/index.json", "Microsoft.EntityFrameworkCore", tc.framework)
			if err != nil {
				t.Fatalf("framework %q: %v", tc.framework, err)
			}
			if got != tc.want {
				t.Errorf("framework %q: version = %q, want %q", tc.framework, got, tc.want)
			}
		})
	}
}

func TestResolveVersionForFrameworkFallsBackWhenNoCompatibleVersion(t *testing.T) {
	srv := newTestRegistryServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"versions":["11.0.0"]}`))
	}))
	got, err := ResolveVersionForFramework(srv.URL+"/{id}/index.json", "demo.pkg", "net10.0")
	if err != nil || got != "11.0.0" {
		t.Fatalf("version = %q, %v; want 11.0.0, nil", got, err)
	}
}

func TestResolveVersionErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"registry error", http.StatusInternalServerError, `{}`, "registry returned 500"},
		{"malformed json", http.StatusOK, `{not json`, ""},
		{"no stable versions", http.StatusOK, `{"versions":["1.0.0-preview","2.0.0-rc.1"]}`, "no stable versions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newTestRegistryServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))

			got, err := ResolveVersionForFramework(srv.URL+"/{id}.json", "Some.Package", "")
			if err == nil {
				t.Fatalf("expected an error, got version %q", got)
			}
			if c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestResolveVersionUnreachableRegistry(t *testing.T) {
	srv := newTestRegistryServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening anymore

	if _, err := ResolveVersionForFramework(url+"/{id}", "Any", ""); err == nil {
		t.Fatal("expected a connection error for a dead registry")
	}
}

// A body larger than the cap must be rejected, not decoded as a truncated
// prefix. The old io.LimitReader decoder silently accepted this response.
func TestResolveVersionRejectsOversizedBody(t *testing.T) {
	srv := newTestRegistryServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"versions":["1.0.0"]}`))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxRegistryResponseBytes)))
	}))

	got, err := ResolveVersionForFramework(srv.URL+"/{id}.json", "demo.pkg", "")
	if err == nil {
		t.Fatalf("oversized response accepted, version = %q", got)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %q, want a response-size error", err.Error())
	}
}
