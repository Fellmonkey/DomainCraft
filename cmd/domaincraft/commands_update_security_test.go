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
	"runtime"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/netutil"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

const (
	assetURLBase = "https://github.com/" + coreRepo + "/releases/download/v1.2.0/"
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

// stubUpdateRun installs offline dependencies for the self-update path.
func stubUpdateRun(t *testing.T, current string, serve func(string) ([]byte, error)) (*bytes.Buffer, *int, *[]byte, updateOps) {
	t.Helper()
	oldVersion := version
	version = current
	t.Cleanup(func() { version = oldVersion })

	calls := 0
	var installed []byte
	ops := defaultUpdateOps()
	ops.getBody = serve
	ops.executable = func() (string, error) { return filepath.Join(t.TempDir(), "domaincraft"), nil }
	ops.replaceExecutable = func(tmp, _ string) error {
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
	return out, &calls, &installed, ops
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
	// D3: all callers now use the strictest format — scheme and host only.
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"credentials and query", "https://user:secret@github.com/dc?token=abc#f", "https://github.com"},
		{"plain", updateAssetURL("dc"), "https://github.com"},
		{"port is dropped", "https://github.com:8443/dc", "https://github.com"},
		{"malformed", "https://[::1", "<invalid url>"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := netutil.RedactedURL(tt.raw); got != tt.want {
				t.Errorf("netutil.RedactedURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateReleaseRedirectRejectsCredentials(t *testing.T) {
	u, err := url.Parse("https://user:secret@objects.githubusercontent.com/asset")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseRedirect(u); err == nil {
		t.Fatal("redirect with userinfo was accepted")
	}
	clean, err := url.Parse("https://objects.githubusercontent.com/asset?sig=abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseRedirect(clean); err != nil {
		t.Fatalf("signed asset redirect rejected: %v", err)
	}
}

func TestReadResponseBodyRejectsOversizedPayload(t *testing.T) {
	limit := int64(4)

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
			got, err := netutil.ReadBoundedBody(resp, u.String(), limit, validateReleaseRedirect)
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
			_, err = netutil.ReadBoundedBody(resp, updateAssetURL("dc"), defaultUpdateOps().maxBodyBytes, validateReleaseRedirect)
			if tt.wantErrPart == "" {
				if err != nil {
					t.Fatalf("netutil.ReadBoundedBody: %v", err)
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
	oldClient, oldCtx := httpClient, bridge.CommandContext()
	t.Cleanup(func() {
		httpClient = oldClient
		bridge.SetCommandContext(oldCtx)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bridge.SetCommandContext(ctx)
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
			ops := defaultUpdateOps()
			ops.getBody = fakeUpdateServer("v1.2.0", assets, map[string][]byte{
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
			got, err := releaseChecksumWithOps(ops, rel, asset)
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
			_, calls, installed, ops := stubUpdateRun(t, "v1.0.0", fakeUpdateServer("v1.2.0", tt.assets, bodies))
			err := runCoreUpdateWithOps(ops, logger.New(), false)
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

// --- F03: failed installs must be reported, never presented as success ---

func stubFileOps(t *testing.T) updateOps {
	t.Helper()
	return defaultUpdateOps()
}

func updatePayload(binary []byte) (map[string]string, map[string][]byte) {
	asset := assetName()
	assets := map[string]string{
		asset:              updateAssetURL(asset),
		checksumsAssetName: updateAssetURL(checksumsAssetName),
	}
	bodies := map[string][]byte{
		updateAssetURL(asset):              binary,
		updateAssetURL(checksumsAssetName): []byte(digestHex(string(binary)) + "  " + asset + "\n"),
	}
	return assets, bodies
}

func TestDownloadBinaryChmodFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit is not set on Windows")
	}
	ops := stubFileOps(t)
	ops.getBody = func(string) ([]byte, error) { return []byte("binary"), nil }

	chmodErr := errors.New("chmod denied")
	ops.chmod = func(string, os.FileMode) error { return chmodErr }
	path, err := downloadBinaryWithOps(ops, "https://github.com/DomainCraft/DomainCraft/releases/download/v1.2.3/domaincraft-linux-amd64", t.TempDir())
	if !errors.Is(err, chmodErr) || !strings.Contains(err.Error(), "make downloaded update executable") {
		t.Fatalf("err = %v, want chmod failure", err)
	}
	if path != "" {
		t.Errorf("path = %q, want no installed binary", path)
	}
}

// TestReplaceExecutableUsesSameVolumeTemp guards the EXDEV fix: the download
// temp must be created next to the running executable, not in the OS temp dir.
func TestReplaceExecutableUsesSameVolumeTemp(t *testing.T) {
	oldVersion := version
	t.Cleanup(func() { version = oldVersion })
	ops := defaultUpdateOps()

	root := t.TempDir()
	exe := filepath.Join(root, "domaincraft")
	assets, bodies := updatePayload([]byte("new binary"))
	version = "1.0.0"
	ops.getBody = fakeUpdateServer("v1.2.0", assets, bodies)
	ops.executable = func() (string, error) { return exe, nil }

	var tempDir, installedFrom string
	ops.createTemp = func(dir, pattern string) (*os.File, error) {
		tempDir = dir
		return os.CreateTemp(dir, pattern)
	}
	ops.replaceExecutable = func(tmp, gotExe string) error {
		installedFrom = tmp
		if gotExe != exe {
			t.Errorf("replace target = %q, want %q", gotExe, exe)
		}
		return nil
	}

	log := logger.New()
	log.SetWriter(io.Discard)
	if err := runCoreUpdateWithOps(ops, log, false); err != nil {
		t.Fatalf("runCoreUpdate: %v", err)
	}
	if tempDir != root {
		t.Fatalf("CreateTemp dir = %q, want the executable directory %q", tempDir, root)
	}
	if filepath.Dir(installedFrom) != root {
		t.Errorf("temp binary %q is not next to the executable %q", installedFrom, exe)
	}
}

func TestReplaceExecutableJoinsRenameFailures(t *testing.T) {
	ops := stubFileOps(t)
	root := t.TempDir()
	tmp, exe := filepath.Join(root, "new"), filepath.Join(root, "domaincraft")
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	directErr := errors.New("direct rename failed")
	installErr := errors.New("install rename failed")
	restoreErr := errors.New("restore failed")
	calls := 0
	ops.rename = func(src, dst string) error {
		calls++
		switch calls {
		case 1:
			return directErr
		case 2:
			return os.Rename(src, dst) // move the locked binary aside
		case 3:
			return installErr
		case 4:
			return restoreErr
		default:
			t.Fatalf("unexpected rename call %d", calls)
			return nil
		}
	}

	err := replaceExecutableWithOps(ops, tmp, exe)
	if err == nil {
		t.Fatal("expected a hard error")
	}
	for _, want := range []string{directErr.Error(), installErr.Error(), restoreErr.Error(), exe + ".old"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err.Error(), want)
		}
	}
}

func TestReplaceExecutableRestoresOriginalOnInstallFailure(t *testing.T) {
	ops := stubFileOps(t)
	root := t.TempDir()
	tmp, exe := filepath.Join(root, "new"), filepath.Join(root, "domaincraft")
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	ops.rename = func(src, dst string) error {
		calls++
		if calls == 1 || calls == 3 {
			return errors.New("rename failed")
		}
		return os.Rename(src, dst)
	}
	err := replaceExecutableWithOps(ops, tmp, exe)
	if err == nil || !strings.Contains(err.Error(), exe+".old") {
		t.Fatalf("err = %v, want failure naming the backup path", err)
	}
	if got := string(mustReadFile(t, exe)); got != "old" {
		t.Errorf("original binary was not restored: %q", got)
	}
}

func TestReplaceExecutableRemoveOldFailure(t *testing.T) {
	ops := stubFileOps(t)
	root := t.TempDir()
	tmp, exe := filepath.Join(root, "new"), filepath.Join(root, "domaincraft")
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	ops.remove = func(path string) error {
		if path == exe+".old" {
			return errors.New("backup is locked")
		}
		return os.Remove(path)
	}
	ops.rename = func(src, _ string) error {
		if src == tmp {
			return errors.New("locked")
		}
		t.Error("no rename should happen when the backup cannot be removed")
		return nil
	}
	err := replaceExecutableWithOps(ops, tmp, exe)
	if err == nil || !strings.Contains(err.Error(), exe+".old") || !strings.Contains(err.Error(), "backup is locked") {
		t.Fatalf("err = %v, want backup removal failure naming %s", err, exe+".old")
	}
}

func TestReplaceExecutableFinalCleanupFailure(t *testing.T) {
	ops := stubFileOps(t)
	root := t.TempDir()
	tmp, exe := filepath.Join(root, "new"), filepath.Join(root, "domaincraft")
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	renames := 0
	ops.rename = func(src, dst string) error {
		renames++
		if renames == 1 {
			return errors.New("locked")
		}
		return os.Rename(src, dst)
	}
	removals := 0
	ops.remove = func(path string) error {
		removals++
		if removals == 2 {
			return errors.New("backup cleanup failed")
		}
		return os.Remove(path)
	}
	err := replaceExecutableWithOps(ops, tmp, exe)
	if err == nil || !strings.Contains(err.Error(), exe+".old") {
		t.Fatalf("err = %v, want cleanup failure naming %s", err, exe+".old")
	}
	if got := string(mustReadFile(t, exe)); got != "new" {
		t.Errorf("new binary was not installed: %q", got)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCoreUpdateLogsSuccessOnlyAfterReplace(t *testing.T) {
	assets, bodies := updatePayload([]byte("new-binary-content"))

	t.Run("failed replace", func(t *testing.T) {
		_, _, _, ops := stubUpdateRun(t, "v1.0.0", fakeUpdateServer("v1.2.0", assets, bodies))
		replaceErr := errors.New("disk full")
		calls := 0
		ops.replaceExecutable = func(string, string) error {
			calls++
			return replaceErr
		}
		var logOut bytes.Buffer
		log := logger.New()
		log.SetWriter(&logOut)
		err := runCoreUpdateWithOps(ops, log, false)
		if !errors.Is(err, replaceErr) {
			t.Fatalf("err = %v, want %v", err, replaceErr)
		}
		if strings.Contains(logOut.String(), "Updated to") {
			t.Errorf("success logged before the binary was installed: %q", logOut.String())
		}
		if calls != 1 {
			t.Errorf("replace calls = %d, want 1", calls)
		}
	})

	t.Run("successful replace", func(t *testing.T) {
		_, calls, installed, ops := stubUpdateRun(t, "v1.0.0", fakeUpdateServer("v1.2.0", assets, bodies))
		var logOut bytes.Buffer
		log := logger.New()
		log.SetWriter(&logOut)
		err := runCoreUpdateWithOps(ops, log, false)
		if err != nil {
			t.Fatalf("runCoreUpdate: %v", err)
		}
		if *calls != 1 || string(*installed) != "new-binary-content" {
			t.Fatalf("replace calls = %d, installed = %q", *calls, *installed)
		}
		if !strings.Contains(logOut.String(), "Updated to v1.2.0") {
			t.Errorf("missing success message: %q", logOut.String())
		}
	})
}
