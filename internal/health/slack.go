package health

import (
	"context"
	"slices"
	"strings"

	"github.com/binarycat0/watchcat/internal/slack"
)

// SlackBot checks the two things Slack notifications need: a working bot
// token and a recipient. Both are always listed, set or not.
type SlackBot struct {
	Bot         *slack.Bot // nil when no token is configured
	UserID      string     // slack.user_id: DMs from the bot
	Channel     string     // slack.channel: used when UserID is empty
	TokenSource string     // where the token came from (secrets.From*)
	StoreErr    error      // secret store problem, if any
}

func (c SlackBot) Run(ctx context.Context) Result {
	r := Result{Name: "slack", Title: "Slack bot", Required: true, Status: OK}
	var problems []string
	fail := func(label, value, problem string) {
		r.addS(label, value, Fail)
		r.worsen(Fail)
		problems = append(problems, problem)
	}

	// 1. Bot token: set, accepted by Slack, allowed to post.
	var id *slack.Identity
	switch {
	case c.Bot == nil || c.Bot.Token == "":
		fail("bot token", "not set: run `watchcat secret set slack-token`", "bot token not set")
	default:
		got, err := c.Bot.AuthTest(ctx)
		if err != nil {
			fail("bot token", "set ("+c.TokenSource+"), rejected or Slack unreachable: "+err.Error(), "bot token rejected: "+err.Error())
			break
		}
		id = &got
		if len(id.Scopes) > 0 && !slices.Contains(id.Scopes, "chat:write") {
			fail("bot token", "set ("+c.TokenSource+"), valid, missing the chat:write scope", "bot token lacks the chat:write scope")
		} else {
			r.add("bot token", "set ("+c.TokenSource+"), valid")
		}
	}

	// 2. Recipient: a user for DMs, or a channel the bot was invited to.
	switch {
	case c.UserID != "":
		r.add("recipient", "DM to user "+c.UserID+" (slack.user_id)")
	case c.Channel != "":
		r.add("recipient", "channel "+c.Channel+" (slack.channel)")
	default:
		fail("recipient", "not set: set slack.user_id (DMs) or slack.channel", "recipient not set")
	}

	if c.StoreErr != nil {
		r.addS("keychain", c.StoreErr.Error(), Fail)
		r.worsen(Fail)
	}
	if id != nil {
		r.add("workspace", id.Team+" ("+id.URL+")")
		r.add("bot", id.User+" ("+id.UserID+", "+id.BotID+")")
		if len(id.Scopes) > 0 {
			r.add("scopes", strings.Join(id.Scopes, ", "))
		}
	}

	switch {
	case len(problems) > 0:
		r.Summary = strings.Join(problems, "; ")
	case id != nil:
		r.Summary = "authenticated as " + id.User + " in " + id.Team
	}
	if c.TokenSource == "config.json (plaintext)" {
		r.worsen(Warn)
		r.Summary += "; token is plaintext in config.json, move it with `watchcat secret set slack-token`"
	}
	return r
}
