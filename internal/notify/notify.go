// Package notify delivers state-change and certificate alerts to chat services.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Kind describes why an event was raised.
type Kind string

const (
	KindDown         Kind = "down"
	KindRecovered    Kind = "recovered"
	KindCertExpiring Kind = "cert_expiring"
)

// Event is the information a notifier needs to describe what happened.
type Event struct {
	Kind         Kind
	Target       string
	URL          string
	Error        string
	StatusCode   int
	Latency      time.Duration
	CertDaysLeft int
	Time         time.Time
}

// Notifier sends an Event somewhere. Implementations must be safe for
// concurrent use because every target runs in its own goroutine.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, e Event) error
}

// Multi fans an event out to several notifiers and joins their errors.
type Multi []Notifier

// Name implements Notifier.
func (m Multi) Name() string { return "multi" }

// Notify implements Notifier. A failing notifier does not stop the others.
func (m Multi) Notify(ctx context.Context, e Event) error {
	var errs []error
	for _, n := range m {
		if err := n.Notify(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// Message renders a short, human readable plain-text message for an event.
func Message(e Event) string {
	var b strings.Builder
	switch e.Kind {
	case KindDown:
		fmt.Fprintf(&b, "🔴 DOWN: %s\n%s\n", e.Target, e.URL)
		if e.StatusCode != 0 {
			fmt.Fprintf(&b, "Status: %d\n", e.StatusCode)
		}
		if e.Error != "" {
			fmt.Fprintf(&b, "Error: %s\n", e.Error)
		}
	case KindRecovered:
		fmt.Fprintf(&b, "🟢 RECOVERED: %s\n%s\n", e.Target, e.URL)
		fmt.Fprintf(&b, "Status: %d, latency: %s\n", e.StatusCode, e.Latency.Round(time.Millisecond))
	case KindCertExpiring:
		fmt.Fprintf(&b, "⚠️ TLS certificate for %s expires in %d day(s)\n%s\n", e.Target, e.CertDaysLeft, e.URL)
	default:
		fmt.Fprintf(&b, "%s: %s\n", e.Kind, e.Target)
	}
	if !e.Time.IsZero() {
		fmt.Fprintf(&b, "Time: %s", e.Time.UTC().Format(time.RFC3339))
	}
	return strings.TrimRight(b.String(), "\n")
}

// postJSON sends v as a JSON body and treats any non-2xx response as an error.
func postJSON(ctx context.Context, client *http.Client, url string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func defaultClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }
