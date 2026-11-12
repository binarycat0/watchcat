package core

import (
	"context"
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if Percentile(v, 50) != 5 || Percentile(v, 95) != 10 || Percentile(v, 0) != 1 {
		t.Fatalf("p50=%v p95=%v", Percentile(v, 50), Percentile(v, 95))
	}
	if Percentile([]float64{7}, 95) != 7 || Percentile(nil, 50) != 0 {
		t.Fatal("edge cases")
	}
}

func at(days int) *time.Time {
	t := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, days)
	return &t
}

func TestBuildReport(t *testing.T) {
	prs := []PR{
		{ID: 1, Host: "github.com", Owner: "o", Repo: "r", Number: 1, Snapshot: &Snapshot{State: "MERGED", OpenedAt: at(0), MergedAt: at(1)}},
		{ID: 2, Host: "github.com", Owner: "o", Repo: "r", Number: 2, Snapshot: &Snapshot{State: "MERGED", OpenedAt: at(0), MergedAt: at(3)}},
		{ID: 3, Host: "github.com", Owner: "o", Repo: "r", Number: 3, Snapshot: &Snapshot{State: "MERGED"}}, // times not fetched yet
		{ID: 4, Host: "github.com", Owner: "o", Repo: "r", Number: 4, Snapshot: &Snapshot{State: "OPEN", OpenedAt: at(2)}},
		{ID: 5, Host: "github.com", Owner: "o", Repo: "r", Number: 5, Snapshot: &Snapshot{State: "CLOSED", ClosedAt: at(-30)}},
		{ID: 6, Host: "github.com", Owner: "o", Repo: "r", Number: 6, Paused: true},
	}
	failures := map[int64]int{1: 2, 2: 5, 4: 1}
	now := *at(10)

	r := BuildReport(prs, failures, nil, now)
	if r.Watched != 6 || r.Merged != 3 || r.Open != 2 || r.Closed != 1 || r.Paused != 1 || r.MissingTimes != 1 {
		t.Fatalf("counts: %+v", r)
	}
	day := 86400.0
	if r.TimeToMerge.Count != 2 || r.TimeToMerge.P50 != 1*day || r.TimeToMerge.P95 != 3*day {
		t.Fatalf("time to merge: %+v", r.TimeToMerge)
	}
	// failures over all 6 PRs: 0,0,0,1,2,5
	if r.CIFailures.Count != 6 || r.CIFailures.P50 != 0 || r.CIFailures.P95 != 5 || r.CIFailures.Max != 5 {
		t.Fatalf("ci failures: %+v", r.CIFailures)
	}

	since := at(-7)
	r = BuildReport(prs, failures, since, now)
	if r.Watched != 5 || r.Closed != 0 {
		t.Fatalf("PR closed before the period should be left out: %+v", r)
	}
}

func TestCIFailureCounts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, _ := s.AddPR(ctx, PRRef{"github.com", "o", "r", 1})
	b, _ := s.AddPR(ctx, PRRef{"github.com", "o", "r", 2})
	s.AddEvent(ctx, a.ID, ChangeCIFailed, "x")
	s.AddEvent(ctx, a.ID, ChangeCIFailed, "y")
	s.AddEvent(ctx, b.ID, ChangeConflict, "z")
	got, err := s.CIFailureCounts(ctx)
	if err != nil || got[a.ID] != 2 || got[b.ID] != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestAddCIRuns(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	done := func(min int) *time.Time { t := base.Add(time.Duration(min) * time.Minute); return &t }
	runs := []CIRun{
		{PRID: 1, HeadSHA: "a", Name: "build", Result: "failure", StartedAt: base, CompletedAt: done(10)},
		{PRID: 1, HeadSHA: "a", Name: "build", Result: "success", StartedAt: base.Add(20 * time.Minute), CompletedAt: done(50)}, // rerun
		{PRID: 1, HeadSHA: "a", Name: "lint", Result: "success", StartedAt: base, CompletedAt: done(2)},
		{PRID: 2, HeadSHA: "b", Name: "build", StartedAt: base}, // still running
		{PRID: 1, HeadSHA: "a", Name: "lint", Result: ResultSuperseded, StartedAt: base, CompletedAt: done(1)},
	}
	var r Report
	r.AddCIRuns(runs)
	if len(r.Jobs) != 2 || r.Jobs[0].Name != "build" || r.Jobs[1].Duration.Count != 1 || r.Jobs[1].Failures != 0 {
		t.Fatalf("jobs, slowest first: %+v", r.Jobs)
	}
	b := r.Jobs[0]
	if b.Runs != 3 || b.Running != 1 || b.Failures != 1 || b.Duration.Count != 2 || b.Duration.P50 != 600 || b.Duration.P95 != 1800 {
		t.Fatalf("build stats: %+v", b)
	}
	// commit a: 10:00 -> 10:50; commit b still running, so left out
	if r.PipelineTime.Count != 1 || r.PipelineTime.P50 != 3000 {
		t.Fatalf("pipeline time: %+v", r.PipelineTime)
	}
}

func TestCIRunUpsert(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	pr, _ := s.AddPR(ctx, PRRef{"github.com", "o", "r", 1})
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	run := CIRun{HeadSHA: "a", Name: "build", Kind: "status", RunKey: "status:build:1", StartedAt: start}
	s.UpsertCIRuns(ctx, pr.ID, []CIRun{run}) // seen running
	end := start.Add(15 * time.Minute)
	run.Result, run.CompletedAt = "success", &end
	s.UpsertCIRuns(ctx, pr.ID, []CIRun{run, {HeadSHA: "a", Name: "build", Kind: "status", RunKey: "status:build:2", StartedAt: end}})
	got, _ := s.CIRunsForPR(ctx, pr.ID)
	if len(got) != 2 || got[1].Result != "success" || got[1].Duration(time.Now()) != 15*time.Minute {
		t.Fatalf("finished run should update in place, rerun added: %+v", got)
	}
}
