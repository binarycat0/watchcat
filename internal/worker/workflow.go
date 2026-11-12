package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/binarycat0/watchcat/internal/core"
)

// Workflow modes, set per rule in config.json.
const (
	ModeOff    = "off"
	ModeDryRun = "dry-run"
	ModeOn     = "on"
)

// Rule is one PR workflow: when Match holds, Tasks are queued. Match returns
// a state key; a rule fires once per (PR, rule, state key), so it reacts to a
// new commit or a new thread but doesn't repeat on every check.
type Rule struct {
	Name   string
	Action string // short description for history and Slack
	Match  func(s core.Snapshot) (stateKey string, ok bool)
	Tasks  func(pr core.PR, s core.Snapshot, p ActionPayload) []core.NewTask
}

// ActionPayload is shared by the tasks rules create.
type ActionPayload struct {
	Rule    string `json:"rule"`
	DryRun  bool   `json:"dry_run"`
	HeadSHA string `json:"head_sha"`         // the commit the rule saw
	Prompt  string `json:"prompt,omitempty"` // agent.prompt: which prompt to build
}

func green(s core.Snapshot) bool    { return s.State == "OPEN" && s.CI == core.CISuccess }
func approved(s core.Snapshot) bool { return s.ReviewDecision == "APPROVED" }

// Rules are the four workflows, in priority order.
var Rules = []Rule{
	{
		Name:   "merge",
		Action: "merge the PR",
		Match: func(s core.Snapshot) (string, bool) {
			ok := green(s) && !s.Draft && approved(s) && s.Unresolved == 0 && s.Mergeable == "MERGEABLE"
			return s.HeadSHA, ok
		},
		Tasks: func(pr core.PR, s core.Snapshot, p ActionPayload) []core.NewTask {
			return []core.NewTask{{Kind: KindPRMerge, PRID: pr.ID, Payload: p, DedupeKey: fmt.Sprintf("%s:%d", KindPRMerge, pr.ID)}}
		},
	},
	{
		Name:   "review_threads",
		Action: "ask the agent to go through the unresolved conversations and propose next steps",
		Match: func(s core.Snapshot) (string, bool) {
			return fmt.Sprintf("%s:%d", s.HeadSHA, s.Unresolved), green(s) && approved(s) && s.Unresolved > 0
		},
		Tasks: agentTask(PromptReviewThreads),
	},
	{
		Name:   "ci_failed",
		Action: "ask the agent to investigate the CI failure (flaky: rebase on the base branch; real: prepare a fix)",
		Match: func(s core.Snapshot) (string, bool) {
			names := make([]string, len(s.FailedChecks))
			for i, c := range s.FailedChecks {
				names[i] = c.Name
			}
			return s.HeadSHA + ":" + strings.Join(names, ","), s.State == "OPEN" && s.CI == core.CIFailure
		},
		Tasks: agentTask(PromptCIFailed),
	},
	{
		Name:   "needs_review",
		Action: "notify that a review is needed and enable auto-merge",
		Match: func(s core.Snapshot) (string, bool) {
			noApproval := s.ReviewDecision == "" || s.ReviewDecision == "REVIEW_REQUIRED"
			return s.HeadSHA, green(s) && !s.Draft && noApproval
		},
		Tasks: func(pr core.PR, s core.Snapshot, p ActionPayload) []core.NewTask {
			if s.AutoMerge {
				return nil
			}
			return []core.NewTask{{Kind: KindPRAutoMerge, PRID: pr.ID, Payload: p, DedupeKey: fmt.Sprintf("%s:%d", KindPRAutoMerge, pr.ID)}}
		},
	},
}

func agentTask(prompt string) func(core.PR, core.Snapshot, ActionPayload) []core.NewTask {
	return func(pr core.PR, s core.Snapshot, p ActionPayload) []core.NewTask {
		p.Prompt = prompt
		return []core.NewTask{{Kind: KindAgentPrompt, PRID: pr.ID, Payload: p,
			DedupeKey: fmt.Sprintf("%s:%d:%s", KindAgentPrompt, pr.ID, prompt)}}
	}
}

// Workflows evaluates Rules after each PR check.
type Workflows struct {
	Store *core.Store
	Modes Value[map[string]string] // rule name -> off / dry-run / on
	Rules []Rule                   // nil = the package-level Rules
	// Notify, if it returns true, adds a Slack DM for every rule that fires.
	Notify Value[bool]
}

// Evaluate fires every matching rule that hasn't fired for this state yet.
func (w *Workflows) Evaluate(ctx context.Context, pr core.PR, s core.Snapshot) error {
	rules := w.Rules
	if rules == nil {
		rules = Rules
	}
	modes := w.Modes()
	for _, r := range rules {
		mode := modes[r.Name]
		if mode != ModeDryRun && mode != ModeOn {
			continue
		}
		key, ok := r.Match(s)
		if !ok {
			continue
		}
		first, err := w.Store.MarkRuleFired(ctx, pr.ID, r.Name, key, mode)
		if err != nil {
			return err
		}
		if !first {
			continue
		}
		prefix := ""
		if mode == ModeDryRun {
			prefix = "[dry-run] would "
		}
		w.Store.AddEvent(ctx, pr.ID, "rule:"+r.Name, prefix+r.Action)

		payload := ActionPayload{Rule: r.Name, DryRun: mode == ModeDryRun, HeadSHA: s.HeadSHA}
		tasks := r.Tasks(pr, s, payload)
		if w.Notify != nil && w.Notify() {
			tasks = append(tasks, core.NewTask{
				Kind: KindSlackNotify, PRID: pr.ID, MaxAttempts: 5,
				Payload: SlackPayload{Text: FormatRule(pr, s, r, mode)},
			})
		}
		for _, t := range tasks {
			if _, err := w.Store.EnqueueTask(ctx, t); err != nil {
				return err
			}
		}
	}
	return nil
}

var ruleEmoji = map[string]string{
	"merge": ":rocket:", "review_threads": ":speech_balloon:", "ci_failed": ":x:", "needs_review": ":eyes:",
}

// FormatRule renders the Slack DM for a fired rule.
func FormatRule(pr core.PR, s core.Snapshot, r Rule, mode string) string {
	head := map[string]string{
		"merge":          "Ready to merge: CI green, approved, no open conversations.",
		"review_threads": fmt.Sprintf("Approved and green, but %d conversation(s) are unresolved.", s.Unresolved),
		"ci_failed":      "CI failed: " + checkNames(s.FailedChecks) + ".",
		"needs_review":   "CI is green and the PR needs a review.",
	}[r.Name]
	next := "Next: " + r.Action + "."
	if mode == ModeDryRun {
		next = "Dry run, nothing changed. Would " + r.Action + "."
	}
	return fmt.Sprintf("%s <%s|%s> %s\n%s\n%s", ruleEmoji[r.Name], pr.URL(), pr.Ref(), slackEscape(s.Title), head, next)
}

func checkNames(cs []core.Check) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}
