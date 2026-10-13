package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/checker"
)

type fakeSource []checker.Status

func (f fakeSource) Snapshot() []checker.Status { return f }

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	days := 42
	src := fakeSource{
		{Name: "api", URL: "https://api.example.com", State: checker.StateUp, StatusCode: 200, LatencyMs: 87, CertDaysLeft: &days, LastCheck: time.Now()},
		{Name: "db-proxy", URL: "http://10.0.0.5/health", State: checker.StateDown, Error: "connection refused <script>"},
	}
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("fake_metric 1\n")) })
	srv := httptest.NewServer(Handler(src, metrics, "test", slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestRoutes(t *testing.T) {
	srv := newTestServer(t)

	tests := []struct {
		path     string
		wantCode int
		contains []string
	}{
		{"/healthz", 200, []string{"ok"}},
		{"/metrics", 200, []string{"fake_metric 1"}},
		{"/", 200, []string{"1</strong> / 2 targets up", "db-proxy", "cert expires in 42 days", "&lt;script&gt;"}},
		{"/nope", 404, nil},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			code, body := get(t, srv.URL+tt.path)
			if code != tt.wantCode {
				t.Fatalf("status = %d, want %d", code, tt.wantCode)
			}
			for _, s := range tt.contains {
				if !strings.Contains(body, s) {
					t.Errorf("body missing %q", s)
				}
			}
		})
	}
}

func TestAPIStatus(t *testing.T) {
	srv := newTestServer(t)
	code, body := get(t, srv.URL+"/api/status")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}

	var got struct {
		Targets []checker.Status `json:"targets"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Targets) != 2 || got.Targets[1].State != checker.StateDown || *got.Targets[0].CertDaysLeft != 42 {
		t.Errorf("unexpected payload: %s", body)
	}
	if strings.Contains(body, `"last_check":"0001`) {
		t.Error("zero last_check should be omitted")
	}
}
