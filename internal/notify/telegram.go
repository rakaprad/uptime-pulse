package notify

import (
	"context"
	"net/http"
	"strings"
)

// DefaultTelegramAPI is the base URL of the Telegram Bot API.
const DefaultTelegramAPI = "https://api.telegram.org"

// Telegram sends messages through the Telegram Bot API sendMessage method.
type Telegram struct {
	BotToken string
	ChatID   string
	// ThreadID targets a topic in a forum-enabled group. Zero means none.
	ThreadID int
	// APIURL can be overridden in tests. Empty means DefaultTelegramAPI.
	APIURL string
	Client *http.Client
}

type telegramMessage struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	MessageThreadID       int    `json:"message_thread_id,omitempty"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// Name implements Notifier.
func (t *Telegram) Name() string { return "telegram" }

// Notify implements Notifier.
func (t *Telegram) Notify(ctx context.Context, e Event) error {
	base := t.APIURL
	if base == "" {
		base = DefaultTelegramAPI
	}
	client := t.Client
	if client == nil {
		client = defaultClient()
	}

	endpoint := strings.TrimRight(base, "/") + "/bot" + t.BotToken + "/sendMessage"
	msg := telegramMessage{
		ChatID:                t.ChatID,
		Text:                  Message(e),
		MessageThreadID:       t.ThreadID,
		DisableWebPagePreview: true,
	}
	// Plain text (no parse_mode) so URLs and error strings never break Markdown parsing.
	return postJSON(ctx, client, endpoint, msg)
}
