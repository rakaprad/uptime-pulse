package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/config"
)

func target(url string) config.Target {
	return config.Target{
		Name:             "test",
		URL:              url,
		Method:           http.MethodGet,
		Timeout:          time.Second,
		Interval:         time.Minute,
		ExpectedStatus:   http.StatusOK,
		FailureThreshold: 1,
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("status: all systems operational"))
		case "/slow":
			time.Sleep(200 * time.Millisecond)
		case "/error":
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		mutate   func(*config.Target)
		wantUp   bool
		wantErr  string
		wantCode int
	}{
		{"ok", func(t *config.Target) { t.URL += "/ok" }, true, "", 200},
		{"keyword found", func(t *config.Target) { t.URL += "/ok"; t.Keyword = "operational" }, true, "", 200},
		{"keyword missing", func(t *config.Target) { t.URL += "/ok"; t.Keyword = "degraded" }, false, "keyword", 200},
		{"unexpected status", func(t *config.Target) { t.URL += "/error" }, false, "unexpected status 500", 500},
		{"expected non-200", func(t *config.Target) { t.URL += "/error"; t.ExpectedStatus = 500 }, true, "", 500},
		{"timeout", func(t *config.Target) { t.URL += "/slow"; t.Timeout = 50 * time.Millisecond }, false, "deadline exceeded", 0},
	}

	c := New("uptime-pulse-test")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tg := target(srv.URL)
			tt.mutate(&tg)

			res := c.Check(context.Background(), tg)
			if res.Up != tt.wantUp {
				t.Errorf("Up = %v, want %v (err=%q)", res.Up, tt.wantUp, res.Err)
			}
			if res.StatusCode != tt.wantCode {
				t.Errorf("StatusCode = %d, want %d", res.StatusCode, tt.wantCode)
			}
			if !strings.Contains(res.Err, tt.wantErr) {
				t.Errorf("Err = %q, want it to contain %q", res.Err, tt.wantErr)
			}
			if !res.CertExpiry.IsZero() {
				t.Error("plain HTTP should not report a certificate")
			}
		})
	}
}

func TestCheckConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens on this port any more

	res := New("test").Check(context.Background(), target(url))
	if res.Up || res.Err == "" {
		t.Fatalf("expected failure, got %+v", res)
	}
}

func TestCheckTLSCertExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	// srv.Client() trusts the test server's self-signed certificate.
	res := NewWithClient(srv.Client(), "test").Check(context.Background(), target(srv.URL))
	if !res.Up {
		t.Fatalf("expected up, got err %q", res.Err)
	}
	days, ok := res.CertDaysLeft(time.Now())
	if !ok || days <= 0 {
		t.Fatalf("CertDaysLeft = %d, %v; want positive", days, ok)
	}
	if !res.CertExpiry.Equal(srv.Certificate().NotAfter) {
		t.Errorf("CertExpiry = %v, want %v", res.CertExpiry, srv.Certificate().NotAfter)
	}
}
