package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/github"
	"github.com/binarycat0/watchcat/internal/templates"
)

const (
	KindPRMerge     = "pr.merge"
	KindPRAutoMerge = "pr.automerge"
	KindAgentPrompt = "agent.prompt"
)

// PRWriter changes PRs on GitHub; github.GH implements it.
type PRWriter interface {
	Merge(ctx context.Context, ref core.PRRef, method, expectSHA string) error
	EnableAutoMerge(ctx context.Context, ref core.PRRef, method string) error
}

// actionContext loads what every action needs: the payload, the PR and a
// fresh snapshot. Actions re-check GitHub right before acting because the
// queue may have waited, and acting on a stale view is how bad merges happen.
func actionContext(ctx context.Context, store *core.Store, gh github.Fetcher, t core.Task) (ActionPayload, core.PR, core.Snapshot, error) {
	var p ActionPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return p, core.PR{}, core.Snapshot{}, err
	}
	if t.PRID == nil {
		return p, core.PR{}, core.Snapshot{}, errors.New("task without pr_id")
	}
	pr, err := store.GetPR(ctx, *t.PRID)
	if err != nil {
		return p, pr, core.Snapshot{}, err
	}
	s, err := gh.Fetch(ctx, pr.Key())
	return p, pr, s, err
}

func ruleByName(name string) Rule {
	for _, r := range Rules {
		if r.Name == name {
			return r
		}
	}
	return Rule{}
}

// Merge handles pr.merge.
type Merge struct {
	Store  *core.Store
	GitHub github.Fetcher
	Writer PRWriter
	Method Value[string] // squash, merge or rebase
}

func (h *Merge) Handle(ctx context.Context, t core.Task) error {
	p, pr, s, err := actionContext(ctx, h.Store, h.GitHub, t)
	if err != nil {
		return err
	}
	if _, ok := ruleByName("merge").Match(s); !ok || s.HeadSHA != p.HeadSHA {
		h.Store.SetTaskResult(ctx, t.ID, "skipped: PR changed since the rule fired ("+s.Summary()+")")
		return nil
	}
	method := h.Method()
	cmd := fmt.Sprintf("gh pr merge %d -R %s --%s --match-head-commit %s", pr.Number, pr.Key().Slug(), method, s.HeadSHA)
	if p.DryRun {
		return h.Store.SetTaskResult(ctx, t.ID, "dry-run: would run "+cmd)
	}
	if err := h.Writer.Merge(ctx, pr.Key(), method, s.HeadSHA); err != nil {
		return err
	}
	h.Store.AddEvent(ctx, pr.ID, "merged", "merged by watchcat ("+method+")")
	return h.Store.SetTaskResult(ctx, t.ID, "ran "+cmd)
}

// AutoMerge handles pr.automerge.
type AutoMerge struct {
	Store  *core.Store
	GitHub github.Fetcher
	Writer PRWriter
	Method Value[string]
}

func (h *AutoMerge) Handle(ctx context.Context, t core.Task) error {
	p, pr, s, err := actionContext(ctx, h.Store, h.GitHub, t)
	if err != nil {
		return err
	}
	if s.AutoMerge || s.State != "OPEN" {
		return h.Store.SetTaskResult(ctx, t.ID, "skipped: auto-merge already on or PR not open")
	}
	method := h.Method()
	cmd := fmt.Sprintf("gh pr merge %d -R %s --auto --%s", pr.Number, pr.Key().Slug(), method)
	if p.DryRun {
		return h.Store.SetTaskResult(ctx, t.ID, "dry-run: would run "+cmd)
	}
	if err := h.Writer.EnableAutoMerge(ctx, pr.Key(), method); err != nil {
		return err
	}
	h.Store.AddEvent(ctx, pr.ID, "automerge", "auto-merge enabled by watchcat ("+method+")")
	return h.Store.SetTaskResult(ctx, t.ID, "ran "+cmd)
}

// Agent queues a prompt in an AI agent session and returns the agent's ID
// for the message. agent.Opencode implements it.
type Agent interface {
	Prompt(ctx context.Context, session, worktree, text, key string) (string, error)
}

// PRReader fetches the extra context prompts include; github.GH implements it.
type PRReader interface {
	UnresolvedThreads(ctx context.Context, ref core.PRRef) ([]github.Thread, error)
	FailedLog(ctx context.Context, ref core.PRRef, runID string, maxLines int) (string, error)
}

// AgentPrompt handles agent.prompt: it builds the prompt from the PR's
// current state and sends it to the PR's agent session.
type AgentPrompt struct {
	Store  *core.Store
	GitHub github.Fetcher
	Reader PRReader
	Agent  Agent // nil until an agent is configured
	// Templates renders the prompts; nil uses the defaults.
	Templates *templates.Set
}

func (h *AgentPrompt) Handle(ctx context.Context, t core.Task) error {
	p, pr, s, err := actionContext(ctx, h.Store, h.GitHub, t)
	if err != nil {
		return err
	}
	var text string
	switch p.Prompt {
	case PromptCIFailed:
		if s.CI != core.CIFailure {
			return h.Store.SetTaskResult(ctx, t.ID, "skipped: CI is no longer failing")
		}
		text = CIFailedPrompt(h.Templates, pr, s, h.failedLogs(ctx, pr, s))
	case PromptReviewThreads:
		threads, err := h.Reader.UnresolvedThreads(ctx, pr.Key())
		if err != nil {
			return err
		}
		if len(threads) == 0 {
			return h.Store.SetTaskResult(ctx, t.ID, "skipped: no unresolved conversations left")
		}
		text = ReviewThreadsPrompt(h.Templates, pr, s, threads)
	default:
		return fmt.Errorf("unknown prompt %q", p.Prompt)
	}

	if p.DryRun {
		h.Store.AddEvent(ctx, pr.ID, "agent", "[dry-run] prompt prepared, not sent (see the task)")
		return h.Store.SetTaskResult(ctx, t.ID, "dry-run, not sent:\n\n"+text)
	}
	if pr.AgentSession == "" {
		h.Store.SetTaskResult(ctx, t.ID, text)
		return errors.New("no agent session linked to this PR: watchcat pr bind <ref> --session <id>")
	}
	if h.Agent == nil {
		h.Store.SetTaskResult(ctx, t.ID, text)
		return errors.New("no agent configured")
	}
	// agent.prompt tasks are single-attempt: if opencode took the prompt but
	// the reply got lost, a retry would make the agent do the work twice.
	inbox, err := h.Agent.Prompt(ctx, pr.AgentSession, pr.AgentWorktree, text, fmt.Sprintf("task:%d", t.ID))
	if err != nil {
		h.Store.SetTaskResult(ctx, t.ID, "not sent:\n\n"+text)
		return err
	}
	h.Store.AddEvent(ctx, pr.ID, "agent", "prompt queued in session "+pr.AgentSession+" ("+inbox+")")
	return h.Store.SetTaskResult(ctx, t.ID, "queued in "+pr.AgentSession+" as "+inbox+":\n\n"+text)
}

// failedLogs collects the tail of each failed Actions run's log. Errors are
// folded into the text: a prompt without logs is still useful.
func (h *AgentPrompt) failedLogs(ctx context.Context, pr core.PR, s core.Snapshot) string {
	seen := map[string]bool{}
	var b strings.Builder
	for _, c := range s.FailedChecks {
		id := github.ActionsRunID(c.URL)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		log, err := h.Reader.FailedLog(ctx, pr.Key(), id, 120)
		if err != nil {
			fmt.Fprintf(&b, "(could not fetch log for run %s: %v)\n", id, err)
			continue
		}
		fmt.Fprintf(&b, "--- failed jobs of run %s (last 120 lines) ---\n%s\n", id, log)
	}
	return b.String()
}
