// Package netutil holds the shared HTTP primitives used by remote probes and
// downloads. It is stdlib-only and carries no language- or bridge-specific
// policy: callers supply the client, the body cap and their redirect rule.
package netutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// RedactedURL reduces a URL to scheme and host for error messages. Userinfo,
// path, query and fragment are dropped: any of them can carry a secret.
func RedactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return "<invalid url>"
	}
	return u.Scheme + "://" + u.Hostname()
}

// redactURLError drops the raw URL carried by *url.Error while keeping the
// underlying reason available for %w wrapping. Nested url.Errors are unwrapped
// as well, since a transport may add its own URL-bearing error.
func redactURLError(err error) error {
	for {
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || urlErr.Err == nil {
			return err
		}
		err = urlErr.Err
	}
}

// ValidateRedirect checks a redirect target before the client follows it. A
// returned error stops the request.
type ValidateRedirect func(*url.URL) error

// RedirectPolicy turns a ValidateRedirect into an http.Client CheckRedirect
// hook. It runs for every hop before the client issues the follow-up request,
// so a forbidden host is never contacted. Returned errors are redacted: only
// the scheme and hostname of the target are named, never userinfo, path or
// query (which may carry tokens).
func RedirectPolicy(validate ValidateRedirect) func(*http.Request, []*http.Request) error {
	if validate == nil {
		return nil
	}
	return func(req *http.Request, _ []*http.Request) error {
		if err := validate(req.URL); err != nil {
			return fmt.Errorf("redirect from %s: %w", RedactedURL(req.URL.String()), redactURLError(err))
		}
		return nil
	}
}

// ReadBoundedBody reads a successful GET response, capping the body at
// maxBytes. A larger body is an error, never a silently truncated success.
// validateRedirect (when non-nil) sees the final request URL when it differs
// from the original URL, i.e. after a redirect the client followed. A direct
// response to the original URL is not a redirect and is not validated here.
func ReadBoundedBody(resp *http.Response, rawURL string, maxBytes int64, validateRedirect ValidateRedirect) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	scope := RedactedURL(rawURL)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d for %s", resp.StatusCode, scope)
	}
	if validateRedirect != nil && resp.Request != nil && resp.Request.URL != nil &&
		resp.Request.URL.String() != rawURL {
		if err := validateRedirect(resp.Request.URL); err != nil {
			return nil, fmt.Errorf("redirect from %s: %w", scope, redactURLError(err))
		}
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("invalid response limit %d for %s", maxBytes, scope)
	}

	// Read one byte past the cap so an oversized body is rejected instead of
	// being truncated into a plausible-looking manifest or binary.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", scope, redactURLError(err))
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", scope, maxBytes)
	}
	return data, nil
}

// BoundedGet performs a GET bound to ctx, using client and capping the response
// body at maxBytes. All errors name only the redacted scheme+host of rawURL.
// The caller keeps ownership of the client: BoundedGet never mutates it. When
// validateRedirect is non-nil, a shallow copy carries a CheckRedirect hook that
// validates every hop before the follow-up request is issued; ReadBoundedBody
// re-checks the final URL as a second line of defence.
func BoundedGet(ctx context.Context, client *http.Client, rawURL string, maxBytes int64, validateRedirect ValidateRedirect) ([]byte, error) {
	scope := RedactedURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", scope, redactURLError(err))
	}
	if validateRedirect != nil {
		guarded := *client
		guarded.CheckRedirect = RedirectPolicy(validateRedirect)
		client = &guarded
	}
	resp, err := client.Do(req) //nolint:bodyclose // ReadBoundedBody closes the body
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", scope, redactURLError(err))
	}
	return ReadBoundedBody(resp, rawURL, maxBytes, validateRedirect)
}
