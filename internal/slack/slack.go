// Package slack talks to the Slack Web API with a bot token.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Poster sends one message. The worker depends on this interface so tests can
// record messages instead of sending them.
type Poster interface {
	Post(ctx context.Context, channel, text string) error
}

// Bot calls the Slack Web API. The token needs the chat:write scope; to post
// in a channel the bot must be a member, a user ID (U...) sends a DM.
type Bot struct {
	Token   string
	BaseURL string // "" = https://slack.com/api; overridden in tests
	HTTP    *http.Client
}

// call POSTs a JSON body to a Web API method and decodes the reply into out.
// Slack returns HTTP 200 with {"ok": false, "error": "..."} for API errors.
func (b *Bot) call(ctx context.Context, method string, in, out any) (http.Header, error) {
	base := b.BaseURL
	if base == "" {
		base = "https://slack.com/api"
	}
	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+b.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return resp.Header, fmt.Errorf("slack: rate limited (retry after %ss)", resp.Header.Get("Retry-After"))
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return resp.Header, fmt.Errorf("slack: %s: %w", resp.Status, err)
	}
	var status struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.Unmarshal(raw, &status)
	if !status.OK {
		return resp.Header, fmt.Errorf("slack: %s", status.Error)
	}
	if out != nil {
		return resp.Header, json.Unmarshal(raw, out)
	}
	return resp.Header, nil
}

func (b *Bot) Post(ctx context.Context, channel, text string) error {
	_, err := b.call(ctx, "chat.postMessage", map[string]any{"channel": channel, "text": text, "unfurl_links": false}, nil)
	return err
}

// Identity is who the token belongs to, from auth.test.
type Identity struct {
	Team   string   `json:"team"`
	TeamID string   `json:"team_id"`
	URL    string   `json:"url"`
	User   string   `json:"user"` // the bot user's name
	UserID string   `json:"user_id"`
	BotID  string   `json:"bot_id"`
	Scopes []string `json:"-"` // from the X-OAuth-Scopes response header
}

// AuthTest verifies the token and reports which bot and workspace it is for.
func (b *Bot) AuthTest(ctx context.Context) (Identity, error) {
	var id Identity
	h, err := b.call(ctx, "auth.test", map[string]any{}, &id)
	if err != nil {
		return id, err
	}
	for _, s := range strings.Split(h.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			id.Scopes = append(id.Scopes, s)
		}
	}
	return id, nil
}
