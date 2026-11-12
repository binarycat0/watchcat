package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/binarycat0/watchdog/internal/core"
	"github.com/binarycat0/watchdog/internal/github"
)

func snapWith(f func(*core.Snapshot)) core.Snapshot {
	s := core.Snapshot{Title: "T", State: "OPEN", HeadSHA: "sha1", Mergeable: "MERGEABLE", CI: core.CISuccess, BaseRef: "main"}
	f(&s)
	return s
}

func TestRuleMatching(t *testing.T) {
	cases := []struct {
		name string
		s    core.Snapshot
		want []string
	}{
		{"ready", snapWith(func(s *core.Snapshot) { s.ReviewDecision = "APPROVED" }), []string{"merge"}},
		{"approved with threads", snapWith(func(s *core.Snapshot) { s.ReviewDecision = "APPROVED"; s.Unresolved = 2 }), []string{"review_threads"}},
		{"ci failed", snapWith(func(s *core.Snapshot) { s.CI = core.CIFailure }), []string{"ci_failed"}},
		{"needs review", snapWith(func(s *core.Snapshot) { s.ReviewDecision = "REVIEW_REQUIRED" }), []string{"needs_review"}},
		{"draft needs no review", snapWith(func(s *core.Snapshot) { s.Draft = true }), nil},
		{"conflict blocks merge", snapWith(func(s *core.Snapshot) { s.ReviewDecision = "APPROVED"; s.Mergeable = "CONFLICTING" }), nil},
		{"ci running", snapWith(func(s *core.Snapshot) { s.CI = core.CIPending }), nil},
		{"merged", snapWith(func(s *core.Snapshot) { s.State = "MERGED"; s.ReviewDecision = "APPROVED" }), nil},
	}
	for _, c := range cases {
		var got []string
		for _, r := range Rules {
			if _, ok := r.Match(c.s); ok {
				got = append(got, r.Name)
			}
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func allModes(mode string) map[string]string {
	m := map[string]string{}
	for _, r := range Rules {
		m[r.Name] = mode
	}
	return m
}

func TestWorkflowsFireOncePerState(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	w := &Workflows{Store: store, Modes: Fixed(allModes(ModeDryRun)), Notify: Fixed(true)}

	failing := snapWith(func(s *core.Snapshot) { s.CI = core.CIFailure; s.FailedChecks = []core.Check{{Name: "unit"}} })
	w.Evaluate(ctx, pr, failing)
	w.Evaluate(ctx, pr, failing) // same state again
	tasks, _ := store.ListTasks(ctx, core.TaskFilter{})
	if len(tasks) != 2 { // agent.prompt + slack DM, once
		t.Fatalf("expected 2 tasks once, got %d: %+v", len(tasks), tasks)
	}
	var p ActionPayload
	for _, tk := range tasks {
		if tk.Kind == KindAgentPrompt {
			json.Unmarshal(tk.Payload, &p)
		}
	}
	if !p.DryRun || p.Prompt != PromptCIFailed || p.HeadSHA != "sha1" {
		t.Fatalf("payload: %+v", p)
	}

	// New commit, still failing: the rule fires again (new DM), but the
	// first prompt is still queued, so dedupe keeps a second one out.
	failing.HeadSHA = "sha2"
	w.Evaluate(ctx, pr, failing)
	if tasks, _ = store.ListTasks(ctx, core.TaskFilter{}); len(tasks) != 3 {
		t.Fatalf("new commit should fire again, got %d tasks", len(tasks))
	}

	w.Modes = Fixed(allModes(ModeOff))
	failing.HeadSHA = "sha3"
	w.Evaluate(ctx, pr, failing)
	if tasks, _ = store.ListTasks(ctx, core.TaskFilter{}); len(tasks) != 3 {
		t.Fatal("off rules must not fire")
	}
}

type fakeWriter struct{ merged, auto int }

func (f *fakeWriter) Merge(ctx context.Context, ref core.PRRef, method, sha string) error {
	f.merged++
	return nil
}
func (f *fakeWriter) EnableAutoMerge(ctx context.Context, ref core.PRRef, method string) error {
	f.auto++
	return nil
}

func task(t *testing.T, prID int64, p ActionPayload) core.Task {
	b, _ := json.Marshal(p)
	return core.Task{ID: 1, PRID: &prID, Payload: b}
}

func TestMergeAction(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	ready := snapWith(func(s *core.Snapshot) { s.ReviewDecision = "APPROVED" })
	gh := &fakeGitHub{snap: ready}
	w := &fakeWriter{}
	h := &Merge{Store: store, GitHub: gh, Writer: w, Method: Fixed("squash")}

	if err := h.Handle(ctx, task(t, pr.ID, ActionPayload{DryRun: true, HeadSHA: "sha1"})); err != nil || w.merged != 0 {
		t.Fatalf("dry-run must not merge: %v %d", err, w.merged)
	}
	if err := h.Handle(ctx, task(t, pr.ID, ActionPayload{HeadSHA: "old"})); err != nil || w.merged != 0 {
		t.Fatalf("new commit since the rule fired must not merge: %v %d", err, w.merged)
	}
	gh.snap.Unresolved = 1
	if err := h.Handle(ctx, task(t, pr.ID, ActionPayload{HeadSHA: "sha1"})); err != nil || w.merged != 0 {
		t.Fatalf("a new thread must block the merge: %v %d", err, w.merged)
	}
	gh.snap.Unresolved = 0
	if err := h.Handle(ctx, task(t, pr.ID, ActionPayload{HeadSHA: "sha1"})); err != nil || w.merged != 1 {
		t.Fatalf("should merge: %v %d", err, w.merged)
	}

	a := &AutoMerge{Store: store, GitHub: gh, Writer: w, Method: Fixed("squash")}
	a.Handle(ctx, task(t, pr.ID, ActionPayload{DryRun: true}))
	a.Handle(ctx, task(t, pr.ID, ActionPayload{}))
	gh.snap.AutoMerge = true
	a.Handle(ctx, task(t, pr.ID, ActionPayload{}))
	if w.auto != 1 {
		t.Fatalf("auto-merge should be enabled exactly once, got %d", w.auto)
	}
}

type fakeReader struct{}

func (fakeReader) UnresolvedThreads(ctx context.Context, ref core.PRRef) ([]github.Thread, error) {
	return []github.Thread{{Path: "a.go", Line: 3, Comments: []github.Comment{{Author: "rev", Body: "rename this", URL: "u"}}}}, nil
}
func (fakeReader) FailedLog(ctx context.Context, ref core.PRRef, run string, n int) (string, error) {
	return "FAIL TestFoo", nil
}

type fakeAgent struct{ sent []string }

func (f *fakeAgent) Prompt(ctx context.Context, session, worktree, text, key string) (string, error) {
	f.sent = append(f.sent, session+"|"+text)
	return "inbox_1", nil
}

func TestAgentPrompt(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	gh := &fakeGitHub{snap: snapWith(func(s *core.Snapshot) {
		s.CI = core.CIFailure
		s.FailedChecks = []core.Check{{Name: "unit", URL: "https://github.com/o/r/actions/runs/5/job/1"}}
	})}
	ag := &fakeAgent{}
	h := &AgentPrompt{Store: store, GitHub: gh, Reader: fakeReader{}, Agent: ag}

	id, _ := store.EnqueueTask(ctx, core.NewTask{Kind: KindAgentPrompt, PRID: pr.ID})
	tk := task(t, pr.ID, ActionPayload{DryRun: true, Prompt: PromptCIFailed})
	tk.ID = id
	if err := h.Handle(ctx, tk); err != nil || len(ag.sent) != 0 {
		t.Fatalf("dry-run must not send: %v", err)
	}
	stored, _ := store.ListTasks(ctx, core.TaskFilter{})
	if !strings.Contains(stored[0].Result, "FAIL TestFoo") || !strings.Contains(stored[0].Result, "rebase the branch on the latest main") {
		t.Fatalf("dry-run should store the prompt: %q", stored[0].Result)
	}

	tk = task(t, pr.ID, ActionPayload{Prompt: PromptCIFailed})
	if err := h.Handle(ctx, tk); err == nil || !strings.Contains(err.Error(), "no agent session") {
		t.Fatalf("unlinked PR should fail clearly: %v", err)
	}
	store.SetAgentSession(ctx, pr.ID, "ses_1", "/w")
	if err := h.Handle(ctx, task(t, pr.ID, ActionPayload{Prompt: PromptCIFailed})); err != nil || len(ag.sent) != 1 || !strings.HasPrefix(ag.sent[0], "ses_1|") {
		t.Fatalf("should send to the linked session: %v %v", err, ag.sent)
	}

	gh.snap = snapWith(func(s *core.Snapshot) { s.ReviewDecision = "APPROVED"; s.Unresolved = 1 })
	if err := h.Handle(ctx, task(t, pr.ID, ActionPayload{Prompt: PromptReviewThreads})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ag.sent[1], "a.go:3") || !strings.Contains(ag.sent[1], "@rev: rename this") || !strings.Contains(ag.sent[1], "Don't push until I confirm") {
		t.Fatalf("review prompt: %s", ag.sent[1])
	}
}

// TestDryRunNeverSends drives the real pool with every rule in dry-run and a
// linked session, through a CI failure and an approved PR with open threads,
// and checks nothing reaches the agent, GitHub writes, or merges.
func TestDryRunNeverSends(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	store.SetAgentSession(ctx, pr.ID, "ses_1", "/w")

	gh := &fakeGitHub{snap: snapWith(func(s *core.Snapshot) {
		s.CI = core.CIFailure
		s.FailedChecks = []core.Check{{Name: "unit"}}
	})}
	ag := &fakeAgent{}
	wr := &fakeWriter{}
	pool := &Pool{
		Store: store,
		Handlers: map[string]Handler{
			KindPRCheck: &PRCheck{Store: store, GitHub: gh, Interval: Fixed(time.Hour),
				Workflows: &Workflows{Store: store, Modes: Fixed(allModes(ModeDryRun))}},
			KindAgentPrompt: &AgentPrompt{Store: store, GitHub: gh, Reader: fakeReader{}, Agent: ag},
			KindPRMerge:     &Merge{Store: store, GitHub: gh, Writer: wr, Method: Fixed("squash")},
			KindPRAutoMerge: &AutoMerge{Store: store, GitHub: gh, Writer: wr, Method: Fixed("squash")},
		},
		Planners: []Planner{PRPlanner(store, Fixed(time.Hour))},
		Tick:     10 * time.Millisecond,
	}
	go pool.Run(ctx)

	waitIdle := func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			q, _ := store.ListTasks(ctx, core.TaskFilter{Status: core.TaskQueued})
			r, _ := store.ListTasks(ctx, core.TaskFilter{Status: core.TaskRunning})
			all, _ := store.ListTasks(ctx, core.TaskFilter{Kind: KindAgentPrompt})
			if len(q)+len(r) == 0 && len(all) > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("pool never went idle")
	}
	waitIdle() // CI failure -> ci_failed rule

	gh.mu.Lock()
	gh.snap = snapWith(func(s *core.Snapshot) { s.HeadSHA = "sha2"; s.ReviewDecision = "APPROVED"; s.Unresolved = 1 })
	gh.mu.Unlock()
	store.SetNextCheck(ctx, pr.ID, time.Now().Add(-time.Second))
	pool.Poke()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ts, _ := store.ListTasks(ctx, core.TaskFilter{Kind: KindAgentPrompt, Status: core.TaskDone})
		if len(ts) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected two dry-run prompt tasks, got %+v", ts)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(ag.sent) != 0 || wr.merged != 0 || wr.auto != 0 {
		t.Fatalf("dry-run acted: prompts=%v merges=%d automerge=%d", ag.sent, wr.merged, wr.auto)
	}
	ts, _ := store.ListTasks(ctx, core.TaskFilter{Kind: KindAgentPrompt})
	for _, tk := range ts {
		if !strings.HasPrefix(tk.Result, "dry-run, not sent") {
			t.Fatalf("task %d result should say not sent: %q", tk.ID, tk.Result)
		}
	}
}
