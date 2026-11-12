package worker

import (
	"fmt"
	"strings"

	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/github"
)

// Prompt names carried in ActionPayload.Prompt.
const (
	PromptCIFailed      = "ci_failed"
	PromptReviewThreads = "review_threads"
)

const promptFooter = `
Rules: do not merge the PR, do not force-push branches you don't own, and do
not resolve review conversations yourself. When you're done, reply with a
short summary of what you did and what is left for me.`

func promptHeader(pr core.PR, s core.Snapshot) string {
	return fmt.Sprintf("[watchcat] PR %s: %s\n%s\nHead commit: %s, base branch: %s\n",
		pr.Ref(), s.Title, pr.URL(), s.HeadSHA, orDefault(s.BaseRef, "main"))
}

// CIFailedPrompt asks the agent to triage a CI failure.
func CIFailedPrompt(pr core.PR, s core.Snapshot, logs string) string {
	var b strings.Builder
	b.WriteString(promptHeader(pr, s))
	b.WriteString("\nCI failed on this PR. Failed checks:\n")
	for _, c := range s.FailedChecks {
		fmt.Fprintf(&b, "- %s %s\n", c.Name, c.URL)
	}
	if logs != "" {
		b.WriteString("\n" + logs)
	}
	fmt.Fprintf(&b, `
Please investigate why it failed and act on it:
1. If it is a flaky test or an infrastructure problem unrelated to this PR,
   rebase the branch on the latest %[1]s and push, so CI runs again.
2. If the failure comes from the code or tests in this PR, prepare a fix,
   run the relevant tests locally, and push it.
Tell me which case it was and why.
`, orDefault(s.BaseRef, "main"))
	b.WriteString(promptFooter)
	return b.String()
}

// ReviewThreadsPrompt asks the agent to go through open review conversations.
func ReviewThreadsPrompt(pr core.PR, s core.Snapshot, threads []github.Thread) string {
	var b strings.Builder
	b.WriteString(promptHeader(pr, s))
	fmt.Fprintf(&b, "\nThe PR is approved and CI is green, but %d review conversation(s) are unresolved:\n", len(threads))
	for i, t := range threads {
		loc := t.Path
		if t.Line > 0 {
			loc = fmt.Sprintf("%s:%d", t.Path, t.Line)
		}
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, loc)
		for _, c := range t.Comments {
			fmt.Fprintf(&b, "   @%s: %s\n", c.Author, indent(c.Body, "   "))
		}
		if len(t.Comments) > 0 {
			fmt.Fprintf(&b, "   %s\n", t.Comments[0].URL)
		}
	}
	b.WriteString(`
Please go through each conversation and propose what to do: a code change
(describe it, or prepare it without pushing), a reply to the reviewer, or
"no change needed" with the reason. Don't push until I confirm.
`)
	b.WriteString(promptFooter)
	return b.String()
}

func indent(s, pad string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n"+pad)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
