package checker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/config"
	"github.com/rakaprad/uptime-pulse/internal/metrics"
	"github.com/rakaprad/uptime-pulse/internal/notify"
)

type recordingNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (r *recordingNotifier) Name() string { return "recording" }
func (r *recordingNotifier) Notify(_ context.Context, e notify.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingNotifier) kinds() []notify.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []notify.Kind
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

func TestMonitorAlertsOnStateChanges(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	tg := target(srv.URL)
	tg.FailureThreshold = 2
	cfg := &config.Config{CertWarningDays: 14, Targets: []config.Target{tg}}
	rec := &recordingNotifier{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mon := NewMonitor(cfg, New("test"), rec, metrics.New(), logger)
	ctx := context.Background()

	healthy.Store(true)
	mon.checkOnce(ctx, 0) // unknown -> up, silent
	healthy.Store(false)
	mon.checkOnce(ctx, 0) // 1 failure, below threshold
	if got := mon.Snapshot()[0]; got.State != StateUp || got.ConsecutiveFailures != 1 {
		t.Fatalf("after one failure: %+v", got)
	}
	mon.checkOnce(ctx, 0) // 2 failures -> down
	mon.checkOnce(ctx, 0) // still down, no repeat
	healthy.Store(true)
	mon.checkOnce(ctx, 0) // recovered

	want := []notify.Kind{notify.KindDown, notify.KindRecovered}
	got := rec.kinds()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events = %v, want %v", got, want)
	}

	st := mon.Snapshot()[0]
	if st.State != StateUp || st.StatusCode != 200 || st.Error != "" || st.LastChange.IsZero() {
		t.Errorf("final status = %+v", st)
	}
}

func TestMonitorCertWarning(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	// The httptest certificate is valid for decades, so use a huge window.
	cfg := &config.Config{CertWarningDays: 1_000_000, Targets: []config.Target{target(srv.URL)}}
	rec := &recordingNotifier{}
	mon := NewMonitor(cfg, NewWithClient(srv.Client(), "test"), rec, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mon.checkOnce(context.Background(), 0)
	mon.checkOnce(context.Background(), 0)

	if got := rec.kinds(); len(got) != 1 || got[0] != notify.KindCertExpiring {
		t.Fatalf("events = %v, want one cert_expiring", got)
	}
	if mon.Snapshot()[0].CertDaysLeft == nil {
		t.Error("CertDaysLeft should be set for https targets")
	}
}

func TestMonitorRunStopsOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	tg := target(srv.URL)
	tg.Interval = 10 * time.Millisecond
	tg.Timeout = 5 * time.Millisecond
	cfg := &config.Config{Targets: []config.Target{tg, {Name: "b", URL: srv.URL, Method: "GET", Interval: tg.Interval, Timeout: tg.Timeout, ExpectedStatus: 200, FailureThreshold: 1}}}
	mon := NewMonitor(cfg, New("test"), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { mon.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	for _, s := range mon.Snapshot() {
		if s.LastCheck.IsZero() {
			t.Errorf("target %s was never checked", s.Name)
		}
	}
}
