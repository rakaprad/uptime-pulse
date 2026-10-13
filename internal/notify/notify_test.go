package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var sampleDown = Event{
	Kind:       KindDown,
	Target:     "api",
	URL:        "https://api.example.com/health",
	StatusCode: 503,
	Error:      "unexpected status 503, want 200",
	Time:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
}

// recorder is a fake HTTP server that captures the last request it received.
type recorder struct {
	path   string
	body   []byte
	status int
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s content-type=%q", req.Method, req.Header.Get("Content-Type"))
		}
		r.path = req.URL.Path
		r.body, _ = io.ReadAll(req.Body)
		if r.status != 0 {
			w.WriteHeader(r.status)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTelegramPayload(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)

	tg := &Telegram{BotToken: "123:abc", ChatID: "-10042", ThreadID: 7, APIURL: srv.URL}
	if err := tg.Notify(context.Background(), sampleDown); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if rec.path != "/bot123:abc/sendMessage" {
		t.Errorf("path = %q", rec.path)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if got["chat_id"] != "-10042" {
		t.Errorf("chat_id = %v", got["chat_id"])
	}
	if got["message_thread_id"] != float64(7) {
		t.Errorf("message_thread_id = %v", got["message_thread_id"])
	}
	if text, _ := got["text"].(string); !strings.Contains(text, "DOWN: api") {
		t.Errorf("text = %q", text)
	}
}

func TestTelegramOmitsZeroThreadID(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)

	tg := &Telegram{BotToken: "t", ChatID: "1", APIURL: srv.URL}
	if err := tg.Notify(context.Background(), sampleDown); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if strings.Contains(string(rec.body), "message_thread_id") {
		t.Errorf("message_thread_id should be omitted, body = %s", rec.body)
	}
}

func TestSlackPayload(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)

	s := &Slack{WebhookURL: srv.URL + "/services/T000/B000/XXX"}
	ev := Event{Kind: KindRecovered, Target: "web", URL: "https://example.com", StatusCode: 200, Latency: 123 * time.Millisecond}
	if err := s.Notify(context.Background(), ev); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	var got slackMessage
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if !strings.Contains(got.Text, "RECOVERED: web") || !strings.Contains(got.Text, "123ms") {
		t.Errorf("text = %q", got.Text)
	}
}

func TestNon2xxIsError(t *testing.T) {
	rec := &recorder{status: http.StatusBadRequest}
	srv := rec.server(t)

	err := (&Slack{WebhookURL: srv.URL}).Notify(context.Background(), sampleDown)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected error mentioning 400, got %v", err)
	}
}

func TestMessage(t *testing.T) {
	tests := []struct {
		ev   Event
		want []string
	}{
		{sampleDown, []string{"DOWN: api", "Status: 503", "Error: unexpected status 503", "2026-01-02T03:04:05Z"}},
		{Event{Kind: KindCertExpiring, Target: "web", URL: "https://example.com", CertDaysLeft: 5}, []string{"expires in 5 day(s)"}},
	}
	for _, tt := range tests {
		msg := Message(tt.ev)
		for _, w := range tt.want {
			if !strings.Contains(msg, w) {
				t.Errorf("Message(%s) = %q, missing %q", tt.ev.Kind, msg, w)
			}
		}
	}
}

type fakeNotifier struct {
	name  string
	err   error
	calls int
}

func (f *fakeNotifier) Name() string { return f.name }
func (f *fakeNotifier) Notify(context.Context, Event) error {
	f.calls++
	return f.err
}

func TestMultiContinuesAfterFailure(t *testing.T) {
	a := &fakeNotifier{name: "a", err: errors.New("boom")}
	b := &fakeNotifier{name: "b"}

	err := Multi{a, b}.Notify(context.Background(), sampleDown)
	if err == nil || !strings.Contains(err.Error(), "a: boom") {
		t.Fatalf("err = %v", err)
	}
	if b.calls != 1 {
		t.Errorf("second notifier should still be called, calls = %d", b.calls)
	}
}
