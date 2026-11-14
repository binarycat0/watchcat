package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/slack"
	"github.com/binarycat0/watchcat/internal/templates"
)

// SlackPayload is the JSON payload of a slack.notify task. The text is built
// when the task is enqueued, so a retry sends exactly the same message.
type SlackPayload struct {
	Text string `json:"text"`
}

// SlackNotify posts a slack.notify task's message.
type SlackNotify struct {
	Slack   slack.Poster
	Channel Value[string] // Slack user ID (DM) or channel ID
}

func (h *SlackNotify) Handle(ctx context.Context, t core.Task) error {
	var p SlackPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return err
	}
	target := h.Channel()
	if h.Slack == nil || target == "" {
		return errors.New("slack is not configured: no token, or no slack.user_id / slack.channel")
	}
	return h.Slack.Post(ctx, target, p.Text)
}

// SlackRouter sends a Slack message for every change kind in notifyOn.
func SlackRouter(notifyOn Value[[]string], tpl *templates.Set) Router {
	return func(pr core.PR, snap core.Snapshot, c core.Change) []core.NewTask {
		for _, k := range notifyOn() {
			if k == c.Kind {
				return []core.NewTask{{
					Kind: KindSlackNotify, PRID: pr.ID, MaxAttempts: 5,
					Payload: SlackPayload{Text: FormatChange(tpl, pr, snap, c)},
				}}
			}
		}
		return nil
	}
}

// Routers combines several routers; each change gets the union of their tasks.
func Routers(rs ...Router) Router {
	return func(pr core.PR, snap core.Snapshot, c core.Change) []core.NewTask {
		var out []core.NewTask
		for _, r := range rs {
			out = append(out, r(pr, snap, c)...)
		}
		return out
	}
}

// SlackFormat writes links as Slack mrkdwn: <url|text>.
var SlackFormat = templates.Format{Name: "slack", Link: func(url, text string) string {
	if url == "" {
		return text
	}
	return "<" + url + "|" + text + ">"
}}

// FormatChange renders a change notification (the notify_change template).
// Text values are escaped for Slack, the only provider so far.
func FormatChange(tpl *templates.Set, pr core.PR, snap core.Snapshot, c core.Change) string {
	return tpl.RenderAs(SlackFormat, templates.NotifyChange, templates.ChangeData{
		PR: prData(pr, snap, true), Kind: c.Kind, Message: slackEscape(c.Message), Checks: checks(c.Checks, true),
	})
}

// slackEscape escapes the three characters Slack treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
