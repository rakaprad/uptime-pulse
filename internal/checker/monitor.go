package checker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/config"
	"github.com/rakaprad/uptime-pulse/internal/metrics"
	"github.com/rakaprad/uptime-pulse/internal/notify"
)

// notifyTimeout bounds how long a single alert delivery may take.
const notifyTimeout = 15 * time.Second

// Status is the public, JSON-serialisable view of a target.
type Status struct {
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	State               State     `json:"state"`
	StatusCode          int       `json:"status_code"`
	LatencyMs           int64     `json:"latency_ms"`
	Error               string    `json:"error,omitempty"`
	CertDaysLeft        *int      `json:"cert_days_left,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastCheck           time.Time `json:"last_check,omitzero"`
	LastChange          time.Time `json:"last_change,omitzero"`
}

// Monitor schedules checks for every target and reacts to their results.
type Monitor struct {
	targets  []config.Target
	warnDays int
	checker  *Checker
	notifier notify.Notifier
	metrics  *metrics.Metrics
	log      *slog.Logger

	// trackers[i] is only touched by the goroutine of targets[i].
	trackers []tracker

	mu       sync.RWMutex
	statuses []Status
}

// NewMonitor wires a Monitor. notifier and m may be nil.
func NewMonitor(cfg *config.Config, c *Checker, notifier notify.Notifier, m *metrics.Metrics, log *slog.Logger) *Monitor {
	mon := &Monitor{
		targets:  cfg.Targets,
		warnDays: cfg.CertWarningDays,
		checker:  c,
		notifier: notifier,
		metrics:  m,
		log:      log,
		trackers: make([]tracker, len(cfg.Targets)),
		statuses: make([]Status, len(cfg.Targets)),
	}
	for i, t := range cfg.Targets {
		mon.trackers[i] = newTracker(t.FailureThreshold)
		mon.statuses[i] = Status{Name: t.Name, URL: t.URL, State: StateUnknown}
	}
	return mon
}

// Run starts one goroutine per target and blocks until ctx is cancelled and
// every goroutine has returned.
func (m *Monitor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := range m.targets {
		wg.Go(func() { m.loop(ctx, i) })
	}
	wg.Wait()
}

func (m *Monitor) loop(ctx context.Context, i int) {
	ticker := time.NewTicker(m.targets[i].Interval)
	defer ticker.Stop()

	for {
		m.checkOnce(ctx, i) // check immediately on startup, then on every tick
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Snapshot returns a copy of the current status of every target, in config order.
func (m *Monitor) Snapshot() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, len(m.statuses))
	copy(out, m.statuses)
	return out
}

func (m *Monitor) checkOnce(ctx context.Context, i int) {
	t := m.targets[i]
	res := m.checker.Check(ctx, t)
	if ctx.Err() != nil {
		return // shutting down; a cancelled request says nothing about the target
	}

	tr := &m.trackers[i]
	transition := tr.observe(res.Up)
	daysLeft, hasCert := res.CertDaysLeft(res.CheckedAt)
	certWarn := tr.observeCert(daysLeft, hasCert, m.warnDays)

	m.recordMetrics(t, res)

	m.mu.Lock()
	st := &m.statuses[i]
	if st.State != tr.state {
		st.LastChange = res.CheckedAt
	}
	st.State = tr.state
	st.StatusCode = res.StatusCode
	st.LatencyMs = res.Latency.Milliseconds()
	st.Error = res.Err
	st.ConsecutiveFailures = tr.failures
	st.LastCheck = res.CheckedAt
	st.CertDaysLeft = nil
	if hasCert {
		st.CertDaysLeft = &daysLeft
	}
	m.mu.Unlock()

	log := m.log.With("target", t.Name, "state", tr.state, "status_code", res.StatusCode,
		"latency_ms", res.Latency.Milliseconds())
	if res.Up {
		log.Debug("check succeeded")
	} else {
		log.Warn("check failed", "error", res.Err, "consecutive_failures", tr.failures)
	}

	ev := notify.Event{
		Target:       t.Name,
		URL:          t.URL,
		Error:        res.Err,
		StatusCode:   res.StatusCode,
		Latency:      res.Latency,
		CertDaysLeft: daysLeft,
		Time:         res.CheckedAt,
	}
	switch transition {
	case WentDown:
		ev.Kind = notify.KindDown
		m.send(ctx, ev)
	case Recovered:
		ev.Kind = notify.KindRecovered
		m.send(ctx, ev)
	}
	if certWarn {
		ev.Kind = notify.KindCertExpiring
		m.send(ctx, ev)
	}
}

func (m *Monitor) recordMetrics(t config.Target, res Result) {
	if m.metrics == nil {
		return
	}
	up, result := 0.0, "failure"
	if res.Up {
		up, result = 1, "success"
	}
	m.metrics.Up.WithLabelValues(t.Name).Set(up)
	m.metrics.Checks.WithLabelValues(t.Name, result).Inc()
	m.metrics.Latency.WithLabelValues(t.Name).Observe(res.Latency.Seconds())
	if !res.CertExpiry.IsZero() {
		m.metrics.CertExpiry.WithLabelValues(t.Name).Set(time.Until(res.CertExpiry).Hours() / 24)
	}
}

func (m *Monitor) send(ctx context.Context, ev notify.Event) {
	m.log.Info("state change", "target", ev.Target, "event", ev.Kind)
	if m.notifier == nil {
		return
	}
	// Deliver even if shutdown has started, but never block forever.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	if err := m.notifier.Notify(ctx, ev); err != nil {
		m.log.Error("notification failed", "target", ev.Target, "event", ev.Kind, "error", err)
	}
}
