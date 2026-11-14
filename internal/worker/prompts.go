package worker

import (
	"fmt"

	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/github"
	"github.com/binarycat0/watchcat/internal/templates"
)

// Prompt names carried in ActionPayload.Prompt.
const (
	PromptCIFailed      = "ci_failed"
	PromptReviewThreads = "review_threads"
)

// CIFailedPrompt asks the agent to triage a CI failure.
func CIFailedPrompt(tpl *templates.Set, pr core.PR, s core.Snapshot, logs string) string {
	return tpl.Render(templates.AgentCIFailed, templates.CIFailedData{
		PR: prData(pr, s, false), FailedChecks: checks(s.FailedChecks, false), Logs: logs,
	})
}

// ReviewThreadsPrompt asks the agent to go through open review conversations.
func ReviewThreadsPrompt(tpl *templates.Set, pr core.PR, s core.Snapshot, threads []github.Thread) string {
	d := templates.ReviewThreadsData{PR: prData(pr, s, false)}
	for i, t := range threads {
		loc := t.Path
		if t.Line > 0 {
			loc = fmt.Sprintf("%s:%d", t.Path, t.Line)
		}
		th := templates.Thread{N: i + 1, Path: t.Path, Line: t.Line, Location: loc}
		for _, c := range t.Comments {
			th.Comments = append(th.Comments, templates.Comment{Author: c.Author, Body: c.Body, URL: c.URL})
		}
		if len(t.Comments) > 0 {
			th.URL = t.Comments[0].URL
		}
		d.Threads = append(d.Threads, th)
	}
	return tpl.Render(templates.AgentReviewThreads, d)
}

// prData is the PR as templates see it. Slack templates get text escaped
// for mrkdwn.
func prData(pr core.PR, s core.Snapshot, slack bool) templates.PR {
	esc := func(v string) string { return v }
	if slack {
		esc = slackEscape
	}
	return templates.PR{
		Ref: pr.Ref(), URL: pr.URL(), Title: esc(s.Title), Repo: pr.Owner + "/" + pr.Repo, Number: pr.Number,
		HeadSHA: s.HeadSHA, Base: orDefault(s.BaseRef, "main"),
	}
}

func checks(cs []core.Check, slack bool) []templates.Check {
	out := make([]templates.Check, len(cs))
	for i, c := range cs {
		out[i] = templates.Check{Name: c.Name, URL: c.URL}
		if slack {
			out[i].Name = slackEscape(c.Name)
		}
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
