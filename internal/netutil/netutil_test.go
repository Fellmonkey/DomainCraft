package netutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRedactedURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"userinfo query and fragment", "https://user:secret@github.com/dc?token=abc#f", "https://github.com"},
		{"port and path are dropped", "https://github.com:8443/releases/download/dc", "https://github.com"},
		{"plain", "https://api.github.com/repos/owner/repo", "https://api.github.com"},
		{"malformed", "https://[::1", "<invalid url>"},
		{"no scheme", "github.com/dc", "<invalid url>"},
		{"no host", "https:///dc", "<invalid url>"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactedURL(tt.raw); got != tt.want {
				t.Errorf("RedactedURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func responseFor(t *testing.T, status int, body string, finalURL string) *http.Response {
	t.Helper()
	u, err := url.Parse(finalURL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    &http.Request{URL: u},
	}
}

func TestReadBoundedBodyRejectsOversize(t *testing.T) {
	raw := "https://user:secret@github.com/dc?token=abc"
	for _, tt := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"at the cap", "1234", false},
		{"one byte over", "12345", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadBoundedBody(responseFor(t, http.StatusOK, tt.body, raw), raw, 4, nil) //nolint:bodyclose // ReadBoundedBody closes the body
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exceeds") {
					t.Fatalf("err = %v, want an oversize error", err)
				}
			} else if err != nil || string(got) != tt.body {
				t.Fatalf("body = %q, %v; want %q", got, err, tt.body)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc")) {
				t.Errorf("error leaked credentials: %q", err)
			}
		})
	}
}

func TestReadBoundedBodyChecksRedirect(t *testing.T) {
	raw := "https://user:secret@github.com/dc?token=abc"
	reject := func(u *url.URL) error {
		if u.Hostname() != "github.com" {
			return fmt.Errorf("host %q is not allowed", u.Hostname())
		}
		return nil
	}

	if _, err := ReadBoundedBody(responseFor(t, http.StatusOK, "ok", "https://evil.example/asset"), raw, 16, reject); err == nil { //nolint:bodyclose // ReadBoundedBody closes the body
		t.Fatal("redirect to a foreign host was accepted")
	} else {
		if !strings.Contains(err.Error(), "redirect from") {
			t.Errorf("error = %q, want a redirect error", err)
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc") {
			t.Errorf("error leaked credentials: %q", err)
		}
	}

	got, err := ReadBoundedBody(responseFor(t, http.StatusOK, "ok", "https://github.com/asset?sig=abc"), raw, 16, reject) //nolint:bodyclose // ReadBoundedBody closes the body
	if err != nil || string(got) != "ok" {
		t.Fatalf("allowed redirect: body = %q, err = %v", got, err)
	}
}

func TestReadBoundedBodyRejectsNonOKStatus(t *testing.T) {
	raw := "https://user:secret@github.com/dc?token=abc"
	_, err := ReadBoundedBody(responseFor(t, http.StatusForbidden, "", raw), raw, 16, nil) //nolint:bodyclose // ReadBoundedBody closes the body
	if err == nil || !strings.Contains(err.Error(), "http 403 for https://github.com") {
		t.Fatalf("err = %v, want a redacted status error", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc") {
		t.Errorf("error leaked credentials: %q", err)
	}
}

type contextTransport struct {
	seen context.Context
}

func (tr *contextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.seen = req.Context()
	return nil, errors.New("transport stopped")
}

func TestBoundedGetUsesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr := &contextTransport{}
	client := &http.Client{Transport: tr}

	_, err := BoundedGet(ctx, client, "https://github.com/dc", 16, nil)
	if err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if tr.seen == nil || !errors.Is(tr.seen.Err(), context.Canceled) {
		t.Errorf("request context = %v, want context.Canceled", tr.seen)
	}
}

func TestBoundedGetRedactsTransportErrors(t *testing.T) {
	client := &http.Client{Transport: &contextTransport{}}
	_, err := BoundedGet(context.Background(), client, "https://user:secret@github.com/dc?token=abc", 16, nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc") {
		t.Errorf("error leaked credentials: %q", err)
	}
}

func TestBoundedGetEnforcesCapAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			_, _ = w.Write([]byte("123456789"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	raw := strings.Replace(server.URL, "http://", "http://user:secret@", 1)
	if _, err := BoundedGet(context.Background(), server.Client(), raw+"/big?token=abc", 4, nil); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body: err = %v, want an oversize error", err)
	} else if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc") {
		t.Errorf("error leaked credentials: %q", err)
	}
	if _, err := BoundedGet(context.Background(), server.Client(), raw+"/missing?token=abc", 4, nil); err == nil || !strings.Contains(err.Error(), "http 404 for http://") {
		t.Fatalf("404 response: err = %v, want a redacted status error", err)
	}
}
