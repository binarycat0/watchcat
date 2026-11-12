package health

import (
	"context"
	"slices"
	"strings"

	"github.com/binarycat0/watchcat/internal/slack"
)

// SlackBot checks the configured bot token with auth.test.
type SlackBot struct {
	Bot         *slack.Bot // nil when no token is configured
	Channel     string
	TokenSource string // where the token came from (secrets.From*)
	StoreErr    error  // secret store problem, if any
}

func (c SlackBot) Run(ctx context.Context) Result {
	r := Result{Name: "slack", Title: "Slack bot", Required: true, Status: OK}
	if c.StoreErr != nil {
		r.addS("keychain", c.StoreErr.Error(), Fail)
	}
	if c.Bot == nil || c.Bot.Token == "" {
		return r.set(Fail, "no bot token: run `watchcat secret set slack-token`")
	}
	r.add("token", c.TokenSource)
	id, err := c.Bot.AuthTest(ctx)
	if err != nil {
		return r.set(Fail, "token rejected or Slack unreachable: "+err.Error())
	}
	r.add("workspace", id.Team+" ("+id.URL+")")
	r.add("bot", id.User+" ("+id.UserID+", "+id.BotID+")")
	if len(id.Scopes) > 0 {
		r.add("scopes", strings.Join(id.Scopes, ", "))
	}
	r.Summary = "authenticated as " + id.User + " in " + id.Team
	if len(id.Scopes) > 0 && !slices.Contains(id.Scopes, "chat:write") {
		r.worsen(Fail)
		r.Summary = "token lacks the chat:write scope"
	}
	if c.Channel == "" {
		r.worsen(Fail)
		r.Summary = "no recipient: set slack.user_id (DMs) or slack.channel"
	} else {
		r.add("channel", c.Channel)
	}
	if c.TokenSource == "config.json (plaintext)" {
		r.worsen(Warn)
		r.Summary += "; token is plaintext in config.json, move it with `watchcat secret set slack-token`"
	}
	return r
}

// SlackCLI reports on the optional Slack CLI. Watchcat posts through the bot
// token, so the CLI is informational.
type SlackCLI struct {
	Exec Exec
}

func (c SlackCLI) Run(ctx context.Context) Result {
	r := Result{Name: "slack-cli", Title: "Slack CLI", Required: false, Status: OK}
	if _, err := c.Exec.LookPath("slack"); err != nil {
		return r.set(Skip, "not installed (optional)")
	}
	if out, err := c.Exec.Output(ctx, "slack", "version"); err == nil {
		r.Version = firstLine(out)
	}
	out, err := c.Exec.Output(ctx, "slack", "auth", "list")
	if err != nil {
		return r.set(Warn, "not authorized: run slack login ("+lastLine(err.Error())+")")
	}
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			r.add("auth", l)
			lines++
		}
	}
	if lines == 0 {
		return r.set(Warn, "no workspaces authorized: run slack login")
	}
	r.Summary = "authorized"
	return r
}
