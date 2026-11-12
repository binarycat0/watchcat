package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/binarycat0/watchdog/internal/core"
	"github.com/binarycat0/watchdog/internal/slack"
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
func SlackRouter(notifyOn Value[[]string]) Router {
	return func(pr core.PR, snap core.Snapshot, c core.Change) []core.NewTask {
		for _, k := range notifyOn() {
			if k == c.Kind {
				return []core.NewTask{{
					Kind: KindSlackNotify, PRID: pr.ID, MaxAttempts: 5,
					Payload: SlackPayload{Text: FormatChange(pr, snap, c)},
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

var changeEmoji = map[string]string{
	core.ChangeCIFailed:         ":x:",
	core.ChangeCIPassed:         ":white_check_mark:",
	core.ChangeConflict:         ":warning:",
	core.ChangeConflictResolved: ":ok:",
	core.ChangeChangesRequested: ":memo:",
	core.ChangeApproved:         ":tada:",
	core.ChangeNewComments:      ":speech_balloon:",
	core.ChangePushed:           ":arrow_up:",
	core.ChangeMerged:           ":rocket:",
	core.ChangeClosed:           ":no_entry_sign:",
}

// FormatChange renders a change as Slack mrkdwn, linking the PR and any
// failing checks.
func FormatChange(pr core.PR, snap core.Snapshot, c core.Change) string {
	var b strings.Builder // efficient string concatenation, like io.StringIO
	fmt.Fprintf(&b, "%s <%s|%s> %s\n%s", changeEmoji[c.Kind], pr.URL(), pr.Ref(), slackEscape(snap.Title), slackEscape(c.Message))
	for _, ch := range c.Checks {
		if ch.URL != "" {
			fmt.Fprintf(&b, "\n• <%s|%s>", ch.URL, slackEscape(ch.Name))
		} else {
			fmt.Fprintf(&b, "\n• %s", slackEscape(ch.Name))
		}
	}
	return b.String()
}

// slackEscape escapes the three characters Slack treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
