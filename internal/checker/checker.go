// Package checker performs HTTP health checks and tracks the state of each target.
package checker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/config"
)

// maxBodyBytes caps how much of a response body is read for keyword matching.
const maxBodyBytes = 1 << 20 // 1 MiB

// Result is the outcome of a single check.
type Result struct {
	Up         bool
	StatusCode int
	Latency    time.Duration
	Err        string
	// CertExpiry is the NotAfter of the leaf certificate (zero for plain HTTP).
	CertExpiry time.Time
	CheckedAt  time.Time
}

// CertDaysLeft returns whole days until the certificate expires and whether
// a certificate was seen at all.
func (r Result) CertDaysLeft(now time.Time) (int, bool) {
	if r.CertExpiry.IsZero() {
		return 0, false
	}
	return int(r.CertExpiry.Sub(now).Hours() / 24), true
}

// Checker runs HTTP checks.
type Checker struct {
	client    *http.Client
	userAgent string
}

// New returns a Checker whose transport opens a fresh connection for every
// check, so DNS, TCP and TLS problems are detected instead of hidden by
// connection reuse.
func New(userAgent string) *Checker {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	return NewWithClient(&http.Client{Transport: tr}, userAgent)
}

// NewWithClient lets callers (mostly tests) supply their own HTTP client.
func NewWithClient(client *http.Client, userAgent string) *Checker {
	return &Checker{client: client, userAgent: userAgent}
}

// Check performs one request against the target and evaluates the response.
func (c *Checker) Check(ctx context.Context, t config.Target) Result {
	ctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	res := Result{CheckedAt: time.Now()}
	req, err := http.NewRequestWithContext(ctx, t.Method, t.URL, nil)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	req.Header.Set("User-Agent", c.userAgent)

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		res.Latency = time.Since(start)
		res.Err = err.Error()
		return res
	}
	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		res.CertExpiry = resp.TLS.PeerCertificates[0].NotAfter
	}

	// Read (part of) the body so latency includes transfer time and the
	// keyword can be matched. Reading is skipped only when it is not needed.
	var body []byte
	if t.Keyword != "" {
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	} else {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	}
	res.Latency = time.Since(start)
	if err != nil {
		res.Err = fmt.Sprintf("read body: %v", err)
		return res
	}

	switch {
	case resp.StatusCode != t.ExpectedStatus:
		res.Err = fmt.Sprintf("unexpected status %d, want %d", resp.StatusCode, t.ExpectedStatus)
	case t.Keyword != "" && !bytes.Contains(body, []byte(t.Keyword)):
		res.Err = fmt.Sprintf("keyword %q not found in response body", t.Keyword)
	default:
		res.Up = true
	}
	return res
}
