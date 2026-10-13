package notify

import (
	"context"
	"net/http"
)

// Slack posts messages to a Slack incoming webhook.
type Slack struct {
	WebhookURL string
	Client     *http.Client
}

type slackMessage struct {
	Text string `json:"text"`
}

// Name implements Notifier.
func (s *Slack) Name() string { return "slack" }

// Notify implements Notifier.
func (s *Slack) Notify(ctx context.Context, e Event) error {
	client := s.Client
	if client == nil {
		client = defaultClient()
	}
	return postJSON(ctx, client, s.WebhookURL, slackMessage{Text: Message(e)})
}
