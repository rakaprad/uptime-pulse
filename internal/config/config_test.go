package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
defaults:
  interval: 1m
targets:
  - name: api
    url: https://api.example.com/health
  - name: web
    url: http://example.com
    method: head
    interval: 15s
    timeout: 2s
    expected_status: 204
    failure_threshold: 1
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if cfg.Listen != ":8080" || cfg.CertWarningDays != 14 {
		t.Errorf("global defaults not applied: listen=%q cert_warning_days=%d", cfg.Listen, cfg.CertWarningDays)
	}

	api := cfg.Targets[0]
	if api.Method != "GET" || api.Interval != time.Minute || api.Timeout != 10*time.Second ||
		api.ExpectedStatus != 200 || api.FailureThreshold != 3 {
		t.Errorf("api target defaults wrong: %+v", api)
	}

	web := cfg.Targets[1]
	if web.Method != "HEAD" || web.Interval != 15*time.Second || web.Timeout != 2*time.Second ||
		web.ExpectedStatus != 204 || web.FailureThreshold != 1 {
		t.Errorf("web target overrides wrong: %+v", web)
	}
}

func TestParseExpandsEnv(t *testing.T) {
	t.Setenv("UP_TEST_TOKEN", "123:abc")
	t.Setenv("UP_TEST_HOST", "status.example.com")
	t.Setenv("UP_TEST_THREAD", "42")
	t.Setenv("UP_TEST_WEBHOOK", "https://hooks.example.com/x # not: a comment")

	cfg, err := Parse([]byte(`
targets:
  - name: svc
    url: https://${UP_TEST_HOST}/healthz
    keyword: "costs $5"
notifiers:
  telegram:
    bot_token: ${UP_TEST_TOKEN}
    chat_id: "${UP_TEST_UNSET_VAR}"
    message_thread_id: ${UP_TEST_THREAD}
  slack:
    webhook_url: ${UP_TEST_WEBHOOK}
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Targets[0].URL; got != "https://status.example.com/healthz" {
		t.Errorf("url = %q", got)
	}
	if got := cfg.Targets[0].Keyword; got != "costs $5" {
		t.Errorf("bare $ should be preserved, keyword = %q", got)
	}
	if got := cfg.Notifiers.Telegram.BotToken; got != "123:abc" {
		t.Errorf("bot_token = %q", got)
	}
	if got := cfg.Notifiers.Telegram.MessageThreadID; got != 42 {
		t.Errorf("message_thread_id = %d, want 42", got)
	}
	if got := cfg.Notifiers.Slack.WebhookURL; got != "https://hooks.example.com/x # not: a comment" {
		t.Errorf("values with YAML syntax must not alter the document, webhook_url = %q", got)
	}
	if cfg.Notifiers.Telegram.Enabled() {
		t.Error("telegram should be disabled when chat_id expands to empty")
	}
}

func TestParseValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"empty file", ``, "at least one target"},
		{"no targets", `listen: ":9000"`, "at least one target"},
		{"missing name", "targets:\n  - url: https://example.com", "name is required"},
		{"bad url", "targets:\n  - name: a\n    url: example.com", "absolute http(s) URL"},
		{"duplicate", "targets:\n  - {name: a, url: 'https://a.example'}\n  - {name: a, url: 'https://b.example'}", "duplicate name"},
		{"timeout too long", "targets:\n  - {name: a, url: 'https://a.example', interval: 5s, timeout: 5s}", "must be shorter than interval"},
		{"bad status", "targets:\n  - {name: a, url: 'https://a.example', expected_status: 42}", "not a valid HTTP status"},
		{"unknown key", "targets:\n  - {name: a, url: 'https://a.example', intervall: 5s}", "field intervall not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadShippedConfigs(t *testing.T) {
	// Example configs shipped in the repo must always stay valid.
	for _, path := range []string{"config.example.yaml", "deploy/uptime-pulse.yaml"} {
		t.Run(path, func(t *testing.T) {
			if _, err := Load(filepath.Join("..", "..", path)); err != nil {
				t.Fatalf("%s is invalid: %v", path, err)
			}
		})
	}
}
