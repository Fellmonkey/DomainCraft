package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

const (
	assetURLBase = "https://github.com/DomainCraft/DomainCraft/releases/download/v1.2.0/"
)

func updateAssetURL(name string) string { return assetURLBase + name }

func fakeUpdateServer(tag string, assets map[string]string, bodies map[string][]byte) func(string) ([]byte, error) {
	release := fakeReleases(tag, assets)
	return func(rawURL string) ([]byte, error) {
		if rawURL == releasesLatestURL {
			return release(rawURL)
		}
		if body, ok := bodies[rawURL]; ok {
			return body, nil
		}
		return nil, fmt.Errorf("unexpected url %s", rawURL)
	}
}

// stubUpdateRun installs offline seams for the self-update path.
func stubUpdateRun(t *testing.T, current string, serve func(string) ([]byte, error)) (*bytes.Buffer, *int, *[]byte) {
	t.Helper()
	oldVersion, oldHTTP := version, httpGetBodyFn
	oldExe, oldReplace := executableFn, replaceExecutableFn
	version = current
	httpGetBodyFn = serve

	calls := 0
	var installed []byte
	executableFn = func() (string, error) { return filepath.Join(t.TempDir(), "domaincraft"), nil }
	replaceExecutableFn = func(tmp, _ string) error {
		calls++
		data, err := os.ReadFile(tmp)
		if err != nil {
			return err
		}
		installed = data
		return nil
	}
	out := &bytes.Buffer{}
	log := logger.New()
	log.SetWriter(out)
	t.Cleanup(func() {
		version, httpGetBodyFn = oldVersion, oldHTTP
		executableFn, replaceExecutableFn = oldExe, oldReplace
	})
	return out, &calls, &installed
}

func TestValidateReleaseURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"github asset", updateAssetURL("dc"), false},
		{"api endpoint", releasesLatestURL, false},
		{"plain http", "http://github.com/DomainCraft/DomainCraft/releases/latest", true},
		{"unknown host", "https://evil.example/dc", true},
		{"lookalike host", "https://github.com.evil.example/dc", true},
		{"userinfo", "https://user:secret@github.com/dc", true},
		{"query", updateAssetURL("dc") + "?token=secret", true},
		{"fragment", updateAssetURL("dc") + "#frag", true},
		{"non-standard port", "https://github.com:8443/dc", true},
		{"malformed", "https://[::1", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateReleaseURL(tt.raw)
			if tt.wantErr != (err != nil) {
				t.Fatalf("validateReleaseURL(%q) = %q, %v; want error = %v", tt.raw, got, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.raw {
				t.Errorf("URL = %q, want %q", got, tt.raw)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"credentials and query", "https://user:secret@github.com/dc?token=abc#f", "https://github.com/dc"},
		{"plain", updateAssetURL("dc"), updateAssetURL("dc")},
		{"malformed", "https://[::1", "<invalid-url>"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactURL(tt.raw); got != tt.want {
				t.Errorf("redactURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadResponseBodyRejectsOversizedPayload(t *testing.T) {
	oldLimit := maxResponseBody
	maxResponseBody = 4
	t.Cleanup(func() { maxResponseBody = oldLimit })

	u, err := url.Parse(updateAssetURL("dc"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"exact limit", "1234", "1234", false},
		{"one byte over", "12345", "", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Request:    &http.Request{URL: u},
			}
			got, err := readResponseBody(resp, u.String())
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exceeds") {
					t.Fatalf("err = %v, want oversized error", err)
				}
				return
			}
			if err != nil || string(got) != tt.want {
				t.Fatalf("body = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestReadResponseBodyChecksRedirectHost(t *testing.T) {
	cases := []struct {
		name        string
		finalURL    string
		wantErrPart string
	}{
		{"signed github asset redirect", "https://objects.githubusercontent.com/asset?X-Amz-Signature=abc", ""},
		{"foreign redirect", "https://evil.example/asset", "redirect from"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.finalURL)
			if err != nil {
				t.Fatal(err)
			}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    &http.Request{URL: u},
			}
			_, err = readResponseBody(resp, updateAssetURL("dc"))
			if tt.wantErrPart == "" {
				if err != nil {
					t.Fatalf("readResponseBody: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Fatalf("err = %v, want %q", err, tt.wantErrPart)
			}
		})
	}
}

type contextCheckingTransport struct {
	ctxErr error
	called bool
}

func (tr *contextCheckingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.called = true
	tr.ctxErr = req.Context().Err()
	return nil, errors.New("transport stopped")
}

func TestHTTPGetBodyUsesCommandContext(t *testing.T) {
	oldClient, oldCtx := httpClient, commandCtx
	t.Cleanup(func() { httpClient, commandCtx = oldClient, oldCtx })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	commandCtx = ctx
	transport := &contextCheckingTransport{}
	httpClient = &http.Client{Transport: transport}

	if _, err := httpGetBody(releasesLatestURL); err == nil {
		t.Fatal("expected request error")
	}
	if !transport.called {
		t.Fatal("transport was not called")
	}
	if !errors.Is(transport.ctxErr, context.Canceled) {
		t.Errorf("request context error = %v, want context.Canceled", transport.ctxErr)
	}
}

type fixedStatusTransport struct{ status int }

func (tr fixedStatusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: tr.status,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestHTTPGetBodyRedactsURLInErrors(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	httpClient = &http.Client{Transport: fixedStatusTransport{status: http.StatusForbidden}}

	_, err := httpGetBody("https://user:secret@github.com/dc?token=abc")
	if err == nil {
		t.Fatal("expected http error")
	}
	got := err.Error()
	if strings.Contains(got, "secret") || strings.Contains(got, "token=abc") {
		t.Errorf("error leaked credentials or query: %q", got)
	}
}

func TestReleaseChecksum(t *testing.T) {
	asset := assetName()
	sum := digestHex("binary")
	assets := map[string]string{
		asset:              updateAssetURL(asset),
		checksumsAssetName: updateAssetURL(checksumsAssetName),
	}
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"plain shasum line", fmt.Sprintf("%s  %s\n", sum, asset), sum, false},
		{"binary marker", fmt.Sprintf("%s *%s\n", sum, asset), sum, false},
		{"missing asset", "other  " + sum + "\n", "", true},
		{"malformed digest", "nothex  " + asset + "\n", "", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			oldHTTP := httpGetBodyFn
			t.Cleanup(func() { httpGetBodyFn = oldHTTP })
			httpGetBodyFn = fakeUpdateServer("v1.2.0", assets, map[string][]byte{
				updateAssetURL(checksumsAssetName): []byte(tt.body),
			})

			rel := &githubRelease{
				TagName: "v1.2.0",
				Assets: []struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{
					{Name: asset, BrowserDownloadURL: updateAssetURL(asset)},
					{Name: checksumsAssetName, BrowserDownloadURL: updateAssetURL(checksumsAssetName)},
				},
			}
			got, err := releaseChecksum(rel, asset)
			if tt.wantErr != (err != nil) {
				t.Fatalf("releaseChecksum = %q, %v; want error = %v", got, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("checksum = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"matching digest", digestHex("binary"), false},
		{"wrong digest", digestHex("other"), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyFileSHA256(path, tt.want)
			if tt.wantErr != (err != nil) {
				t.Fatalf("verifyFileSHA256 = %v, want error = %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunCoreUpdateFailsClosed(t *testing.T) {
	asset := assetName()
	goodSum := digestHex("new-binary-content")
	validAssets := map[string]string{
		asset:              updateAssetURL(asset),
		checksumsAssetName: updateAssetURL(checksumsAssetName),
	}
	checksums := []byte(fmt.Sprintf("%s  %s\n", goodSum, asset))

	cases := []struct {
		name        string
		assets      map[string]string
		sums        []byte
		binary      []byte
		wantErrPart string
		wantReplace bool
	}{
		{
			name:        "valid checksum installs binary",
			assets:      validAssets,
			sums:        checksums,
			binary:      []byte("new-binary-content"),
			wantReplace: true,
		},
		{
			name:        "checksum mismatch blocks replacement",
			assets:      validAssets,
			sums:        []byte(fmt.Sprintf("%s  %s\n", digestHex("attacker"), asset)),
			binary:      []byte("new-binary-content"),
			wantErrPart: "checksum mismatch",
		},
		{
			name:        "untrusted asset host blocks replacement",
			assets:      map[string]string{asset: "https://evil.example/dc", checksumsAssetName: updateAssetURL(checksumsAssetName)},
			sums:        checksums,
			binary:      []byte("new-binary-content"),
			wantErrPart: "refusing download",
		},
		{
			name:        "missing checksums asset blocks replacement",
			assets:      map[string]string{asset: updateAssetURL(asset)},
			binary:      []byte("new-binary-content"),
			wantErrPart: "checksums.txt",
		},
		{
			name:        "malformed checksum blocks replacement",
			assets:      validAssets,
			sums:        []byte("not-a-sha  " + asset + "\n"),
			binary:      []byte("new-binary-content"),
			wantErrPart: "invalid sha-256 checksum",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bodies := map[string][]byte{}
			for _, u := range tt.assets {
				if strings.HasSuffix(u, checksumsAssetName) && tt.sums != nil {
					bodies[u] = tt.sums
				}
				if strings.HasSuffix(u, "/"+asset) {
					bodies[u] = tt.binary
				}
			}
			_, calls, installed := stubUpdateRun(t, "v1.0.0", fakeUpdateServer("v1.2.0", tt.assets, bodies))
			err := runCoreUpdate(logger.New(), false)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErrPart)
				}
			} else if err != nil {
				t.Fatalf("runCoreUpdate: %v", err)
			}
			wantCalls := 0
			if tt.wantReplace {
				wantCalls = 1
				if !bytes.Equal(*installed, tt.binary) {
					t.Errorf("installed = %q, want %q", *installed, tt.binary)
				}
			}
			if *calls != wantCalls {
				t.Errorf("replace calls = %d, want %d", *calls, wantCalls)
			}
		})
	}
}
