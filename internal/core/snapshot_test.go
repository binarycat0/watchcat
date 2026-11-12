package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func kinds(cs []Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Kind
	}
	return out
}

func TestDiff(t *testing.T) {
	green := Snapshot{State: "OPEN", HeadSHA: "a", Mergeable: "MERGEABLE", CI: CISuccess}
	failing := green
	failing.CI = CIFailure
	failing.FailedChecks = []Check{{Name: "unit"}}

	cases := []struct {
		name string
		prev *Snapshot
		cur  Snapshot
		want []string
	}{
		{"first check green is silent", nil, green, nil},
		{"first check failing alerts", nil, failing, []string{ChangeCIFailed}},
		{"same failure again is silent", &failing, failing, nil},
		{"recovery", &failing, green, []string{ChangeCIPassed}},
		{"new push failing again alerts", &failing, func() Snapshot { s := failing; s.HeadSHA = "b"; return s }(),
			[]string{ChangePushed, ChangeCIFailed}},
		{"conflict", &green, func() Snapshot { s := green; s.Mergeable = "CONFLICTING"; return s }(), []string{ChangeConflict}},
		{"unknown mergeable is not resolution", func() *Snapshot { s := green; s.Mergeable = "CONFLICTING"; return &s }(),
			func() Snapshot { s := green; s.Mergeable = "UNKNOWN"; return s }(), nil},
		{"new threads", &green, func() Snapshot { s := green; s.Unresolved = 2; return s }(), []string{ChangeNewComments}},
		{"changes requested", &green, func() Snapshot { s := green; s.ReviewDecision = "CHANGES_REQUESTED"; return s }(),
			[]string{ChangeChangesRequested}},
		{"merged stops there", &failing, func() Snapshot { s := failing; s.State = "MERGED"; return s }(), []string{ChangeMerged}},
	}
	for _, c := range cases {
		got := kinds(Diff(c.prev, c.cur))
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v want %v", c.name, got, c.want)
			}
		}
	}
}

func TestStatusPriority(t *testing.T) {
	s := Snapshot{State: "OPEN", Mergeable: "CONFLICTING", CI: CIFailure, Unresolved: 1}
	if s.Status() != StatusConflict {
		t.Fatalf("got %s", s.Status())
	}
	s.Mergeable = "MERGEABLE"
	if s.Status() != StatusCIFailed {
		t.Fatalf("got %s", s.Status())
	}
}

func TestTaskQueue(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pr, _ := s.AddPR(ctx, PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})

	id, _ := s.EnqueueTask(ctx, NewTask{Kind: "k", PRID: pr.ID, DedupeKey: "x", MaxAttempts: 2})
	if id == 0 {
		t.Fatal("first enqueue should insert")
	}
	if dup, _ := s.EnqueueTask(ctx, NewTask{Kind: "k", DedupeKey: "x"}); dup != 0 {
		t.Fatal("dedupe key should block a second pending task")
	}

	task, ok, err := s.ClaimTask(ctx, time.Now())
	if err != nil || !ok || task.ID != id || task.Attempts != 1 || task.Status != TaskRunning {
		t.Fatalf("claim: %+v %v %v", task, ok, err)
	}
	if _, ok, _ := s.ClaimTask(ctx, time.Now()); ok {
		t.Fatal("running task must not be claimed twice")
	}

	// First failure: retried later.
	s.FailTask(ctx, task, context.DeadlineExceeded, time.Now().Add(time.Hour))
	if _, ok, _ := s.ClaimTask(ctx, time.Now()); ok {
		t.Fatal("retry should wait for its run_at")
	}
	task, ok, _ = s.ClaimTask(ctx, time.Now().Add(2*time.Hour))
	if !ok || task.Attempts != 2 {
		t.Fatalf("retry claim: %+v %v", task, ok)
	}
	// Second failure: out of attempts.
	s.FailTask(ctx, task, context.DeadlineExceeded, time.Now())
	failed, _ := s.ListTasks(ctx, TaskFilter{Status: TaskFailed})
	if len(failed) != 1 || failed[0].LastError == "" {
		t.Fatalf("expected one failed task, got %+v", failed)
	}
	// Dedupe key is free again once the task is finished.
	if id, _ := s.EnqueueTask(ctx, NewTask{Kind: "k", DedupeKey: "x"}); id == 0 {
		t.Fatal("dedupe should only cover pending tasks")
	}

	// A task stuck in running after a crash goes back to the queue.
	s.ClaimTask(ctx, time.Now())
	if n, _ := s.RequeueRunning(ctx); n != 1 {
		t.Fatalf("requeued %d", n)
	}
}
