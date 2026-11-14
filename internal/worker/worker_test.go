package worker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binarycat0/watchcat/internal/core"
)

func newStore(t *testing.T) *core.Store {
	t.Helper()
	s, err := core.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// fakeGitHub returns whatever snapshot the test sets next.
type fakeGitHub struct {
	mu   sync.Mutex
	snap core.Snapshot
	err  error
}

func (f *fakeGitHub) Fetch(ctx context.Context, ref core.PRRef) (core.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap, f.err
}

type fakeSlack struct {
	mu   sync.Mutex
	msgs []string
	fail int // fail this many times first
}

func (f *fakeSlack) Post(ctx context.Context, channel, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("boom")
	}
	f.msgs = append(f.msgs, text)
	return nil
}

func TestPRCheckFansOutToSlack(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 7})
	gh := &fakeGitHub{snap: core.Snapshot{Title: "My <PR>", State: "OPEN", HeadSHA: "a", Mergeable: "MERGEABLE",
		CI: core.CIFailure, FailedChecks: []core.Check{{Name: "unit", URL: "https://ci/1"}}}}
	h := &PRCheck{Store: store, GitHub: gh, Interval: Fixed(time.Hour), Route: SlackRouter(Fixed([]string{core.ChangeCIFailed}), nil)}

	run := func() {
		if err := h.Handle(ctx, core.Task{PRID: &pr.ID}); err != nil {
			t.Fatal(err)
		}
	}
	run()
	got, _ := store.GetPR(ctx, pr.ID)
	if got.Status != core.StatusCIFailed || got.Snapshot == nil {
		t.Fatalf("PR after check: %+v", got)
	}
	tasks, _ := store.ListTasks(ctx, core.TaskFilter{Status: core.TaskQueued})
	if len(tasks) != 1 || tasks[0].Kind != KindSlackNotify {
		t.Fatalf("expected one slack task, got %+v", tasks)
	}
	if !strings.Contains(string(tasks[0].Payload), "My \\u0026lt;PR\\u0026gt;") || !strings.Contains(string(tasks[0].Payload), "https://ci/1|unit") {
		t.Fatalf("payload: %s", tasks[0].Payload)
	}

	run() // same state: no new notification
	if tasks, _ = store.ListTasks(ctx, core.TaskFilter{Kind: KindSlackNotify}); len(tasks) != 1 {
		t.Fatalf("unchanged PR should not notify again, got %d", len(tasks))
	}

	gh.snap.State = "MERGED"
	run()
	if got, _ = store.GetPR(ctx, pr.ID); !got.Paused || got.Status != core.StatusMerged {
		t.Fatalf("merged PR should be auto-paused: %+v", got)
	}

	gh.err = errors.New("gh: rate limited")
	if err := h.Handle(ctx, core.Task{PRID: &pr.ID}); err == nil {
		t.Fatal("fetch error should fail the task")
	}
	if got, _ = store.GetPR(ctx, pr.ID); got.Status != core.StatusError || got.Snapshot.State != "MERGED" {
		t.Fatalf("error should keep the last snapshot: %+v", got)
	}
}

func TestPoolRunsPlansAndRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	gh := &fakeGitHub{snap: core.Snapshot{State: "OPEN", Mergeable: "CONFLICTING", CI: core.CISuccess}}
	sl := &fakeSlack{fail: 1}

	pool := &Pool{
		Store: store,
		Handlers: map[string]Handler{
			KindPRCheck:     &PRCheck{Store: store, GitHub: gh, Interval: Fixed(time.Hour), Route: SlackRouter(Fixed([]string{core.ChangeConflict}), nil)},
			KindSlackNotify: &SlackNotify{Slack: sl, Channel: Fixed("C1")},
		},
		Planners:   []Planner{PRPlanner(store, Fixed(time.Hour))},
		Tick:       10 * time.Millisecond,
		RetryDelay: func(int) time.Duration { return 0 }, // retry immediately
	}
	done := make(chan struct{})
	go func() { pool.Run(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		sl.mu.Lock()
		n := len(sl.msgs)
		sl.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slack message never delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ts, _ := store.ListTasks(ctx, core.TaskFilter{Kind: KindSlackNotify})
	if len(ts) != 1 || ts[0].Attempts != 2 {
		t.Fatalf("expected one slack task delivered on attempt 2: %+v", ts)
	}
	if got, _ := store.GetPR(ctx, pr.ID); got.Status != core.StatusConflict {
		t.Fatalf("status %s", got.Status)
	}
	cancel()
	<-done
}

func TestUnknownKindFailsPermanently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := newStore(t)
	store.EnqueueTask(ctx, core.NewTask{Kind: "nope", MaxAttempts: 5})
	pool := &Pool{Store: store, Handlers: map[string]Handler{}, Tick: 10 * time.Millisecond}
	go pool.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		ts, _ := store.ListTasks(ctx, core.TaskFilter{Status: core.TaskFailed})
		if len(ts) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("unknown task kind was not failed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPanicIsCaught(t *testing.T) {
	p := &Pool{Handlers: map[string]Handler{"p": HandlerFunc(func(ctx context.Context, t core.Task) error { panic("bad") })}}
	p.init()
	if err := p.handle(context.Background(), core.Task{Kind: "p"}); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("got %v", err)
	}
}

func TestBackoff(t *testing.T) {
	if Backoff(1) != 30*time.Second || Backoff(3) != 2*time.Minute || Backoff(20) != 30*time.Minute {
		t.Fatal("unexpected backoff")
	}
}

type fakeRetrigger struct {
	runs []string
}

func (f *fakeRetrigger) RerunFailed(ctx context.Context, ref core.PRRef, runID string) error {
	f.runs = append(f.runs, runID)
	return nil
}

func TestRetrigger(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	snap := core.Snapshot{State: "OPEN", CI: core.CIFailure, FailedChecks: []core.Check{
		{Name: "unit", URL: "https://github.com/o/r/actions/runs/111/job/9"},
		{Name: "lint", URL: "https://github.com/o/r/actions/runs/111/job/10"},
		{Name: "jenkins", URL: "https://jenkins/job/1"},
	}}
	store.RecordCheck(ctx, pr.ID, core.CheckResult{Status: core.StatusCIFailed, Snapshot: &snap}, time.Now(), time.Now().Add(time.Hour))

	gh := &fakeRetrigger{}
	h := &Retrigger{Store: store, GitHub: gh}
	err := h.Handle(ctx, core.Task{PRID: &pr.ID})
	if err == nil || !strings.Contains(err.Error(), "skipped jenkins") {
		t.Fatalf("non-Actions check should be reported as skipped: %v", err)
	}
	if len(gh.runs) != 1 || gh.runs[0] != "111" {
		t.Fatalf("one Actions run should be re-run once: %v", gh.runs)
	}
	if ts, _ := store.ListTasks(ctx, core.TaskFilter{Kind: KindPRCheck}); len(ts) != 1 {
		t.Fatalf("a follow-up check should be queued: %+v", ts)
	}
}
