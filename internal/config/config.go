// Package config loads and validates the uptime-pulse YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of the YAML configuration file.
type Config struct {
	Listen          string    `yaml:"listen"`
	LogLevel        string    `yaml:"log_level"`
	LogFormat       string    `yaml:"log_format"`
	CertWarningDays int       `yaml:"cert_warning_days"`
	Defaults        Defaults  `yaml:"defaults"`
	Targets         []Target  `yaml:"targets"`
	Notifiers       Notifiers `yaml:"notifiers"`
}

// Defaults are applied to every target that does not set the field itself.
type Defaults struct {
	Method           string        `yaml:"method"`
	Interval         time.Duration `yaml:"interval"`
	Timeout          time.Duration `yaml:"timeout"`
	ExpectedStatus   int           `yaml:"expected_status"`
	FailureThreshold int           `yaml:"failure_threshold"`
}

// Target is a single endpoint to monitor.
type Target struct {
	Name             string        `yaml:"name"`
	URL              string        `yaml:"url"`
	Method           string        `yaml:"method"`
	Interval         time.Duration `yaml:"interval"`
	Timeout          time.Duration `yaml:"timeout"`
	ExpectedStatus   int           `yaml:"expected_status"`
	Keyword          string        `yaml:"keyword"`
	FailureThreshold int           `yaml:"failure_threshold"`
}

// Notifiers holds the configuration of every supported notifier.
// A notifier is enabled when its required fields are non-empty.
type Notifiers struct {
	Telegram Telegram `yaml:"telegram"`
	Slack    Slack    `yaml:"slack"`
}

// Telegram configures the Telegram Bot API notifier.
type Telegram struct {
	BotToken        string `yaml:"bot_token"`
	ChatID          string `yaml:"chat_id"`
	MessageThreadID int    `yaml:"message_thread_id"`
}

// Enabled reports whether enough fields are set to send messages.
func (t Telegram) Enabled() bool { return t.BotToken != "" && t.ChatID != "" }

// Slack configures the Slack incoming webhook notifier.
type Slack struct {
	WebhookURL string `yaml:"webhook_url"`
}

// Enabled reports whether a webhook URL is configured.
func (s Slack) Enabled() bool { return s.WebhookURL != "" }

var builtinDefaults = Defaults{
	Method:           http.MethodGet,
	Interval:         30 * time.Second,
	Timeout:          10 * time.Second,
	ExpectedStatus:   http.StatusOK,
	FailureThreshold: 3,
}

// Load reads the file at path, expands ${ENV_VAR} references, parses it and
// validates the result.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw)
}

// Parse parses raw YAML, expands ${ENV_VAR} references, applies defaults and
// validates the result.
func Parse(raw []byte) (*Config, error) {
	expanded, err := expandEnv(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true) // typos in keys should fail loudly, not silently

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv replaces ${VAR} with the value of the environment variable VAR.
// Unset variables expand to an empty string. Bare $VAR is left untouched so
// literal dollar signs in URLs or keywords keep working.
func ExpandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envPattern.FindStringSubmatch(m)[1])
	})
}

// expandEnv expands variables inside YAML scalar values rather than in the raw
// text. A secret containing YAML syntax (": ", "#", quotes, ...) therefore can
// never change the structure of the document.
func expandEnv(raw []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode && envPattern.MatchString(n.Value) {
			n.Value = ExpandEnv(n.Value)
			if n.Style == 0 {
				// Unquoted: let the type be re-resolved, so that
				// "message_thread_id: ${THREAD_ID}" still decodes as a number.
				n.Tag = ""
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&root)
	return yaml.Marshal(&root)
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.LogFormat == "" {
		c.LogFormat = "json"
	}
	if c.CertWarningDays == 0 {
		c.CertWarningDays = 14
	}

	d := &c.Defaults
	d.Method = firstNonZero(d.Method, builtinDefaults.Method)
	d.Interval = firstNonZero(d.Interval, builtinDefaults.Interval)
	d.Timeout = firstNonZero(d.Timeout, builtinDefaults.Timeout)
	d.ExpectedStatus = firstNonZero(d.ExpectedStatus, builtinDefaults.ExpectedStatus)
	d.FailureThreshold = firstNonZero(d.FailureThreshold, builtinDefaults.FailureThreshold)

	for i := range c.Targets {
		t := &c.Targets[i]
		t.Method = strings.ToUpper(firstNonZero(t.Method, d.Method))
		t.Interval = firstNonZero(t.Interval, d.Interval)
		t.Timeout = firstNonZero(t.Timeout, d.Timeout)
		t.ExpectedStatus = firstNonZero(t.ExpectedStatus, d.ExpectedStatus)
		t.FailureThreshold = firstNonZero(t.FailureThreshold, d.FailureThreshold)
	}
}

// Validate checks the configuration for mistakes and returns all of them at once.
func (c *Config) Validate() error {
	var errs []error
	if len(c.Targets) == 0 {
		errs = append(errs, errors.New("at least one target is required"))
	}
	if c.CertWarningDays < 0 {
		errs = append(errs, errors.New("cert_warning_days must not be negative"))
	}

	seen := make(map[string]bool)
	for i, t := range c.Targets {
		prefix := fmt.Sprintf("targets[%d]", i)
		if t.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", prefix))
		} else {
			prefix = fmt.Sprintf("target %q", t.Name)
		}
		if seen[t.Name] && t.Name != "" {
			errs = append(errs, fmt.Errorf("%s: duplicate name", prefix))
		}
		seen[t.Name] = true

		u, err := url.Parse(t.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s: url must be an absolute http(s) URL", prefix))
		}
		if t.Timeout >= t.Interval {
			errs = append(errs, fmt.Errorf("%s: timeout (%s) must be shorter than interval (%s)", prefix, t.Timeout, t.Interval))
		}
		if t.ExpectedStatus < 100 || t.ExpectedStatus > 599 {
			errs = append(errs, fmt.Errorf("%s: expected_status %d is not a valid HTTP status", prefix, t.ExpectedStatus))
		}
		if t.FailureThreshold < 1 {
			errs = append(errs, fmt.Errorf("%s: failure_threshold must be >= 1", prefix))
		}
	}
	return errors.Join(errs...)
}

func firstNonZero[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}
