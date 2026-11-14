package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/github"
)

// Task kinds. New background jobs (review resolver, agent hand-off) add a
// kind here, a handler, and a line in Router.
const (
	KindPRCheck     = "pr.check"
	KindSlackNotify = "slack.notify"
)

// PRCheckDedupe is the dedupe key that keeps at most one pending check per PR.
func PRCheckDedupe(prID int64) string { return fmt.Sprintf("%s:%d", KindPRCheck, prID) }

// PRPlanner enqueues a pr.check for every unpaused PR whose next_check is due.
func PRPlanner(store *core.Store, interval Value[time.Duration]) Planner {
	// Returning a closure is Go's way of building a configured function,
	// like functools.partial.
	return func(ctx context.Context, now time.Time) error {
		due, err := store.DuePRs(ctx, now)
		if err != nil {
			return err
		}
		for _, pr := range due {
			if _, err := store.EnqueueTask(ctx, core.NewTask{
				Kind: KindPRCheck, PRID: pr.ID, DedupeKey: PRCheckDedupe(pr.ID),
			}); err != nil {
				return err
			}
			// Push next_check out so the next tick doesn't enqueue it again;
			// the check itself sets the real next time when it finishes.
			if err := store.SetNextCheck(ctx, pr.ID, now.Add(interval())); err != nil {
				return err
			}
		}
		return nil
	}
}

// Router decides which follow-up tasks a change triggers. This is the single
// place where future workers (review resolver, agent hand-off) get wired in.
type Router func(pr core.PR, snap core.Snapshot, c core.Change) []core.NewTask

// PRCheck fetches a PR from GitHub, records its state, diffs it against the
// previous snapshot and enqueues follow-ups for each change.
type PRCheck struct {
	Store     *core.Store
	GitHub    github.Fetcher
	Interval  Value[time.Duration]
	Route     Router
	Workflows *Workflows   // nil = no workflow rules
	Runs      CIRunFetcher // nil = don't record CI run durations
}

// CIRunFetcher lists the CI job runs on a commit; github.GH implements it.
type CIRunFetcher interface {
	CIRuns(ctx context.Context, ref core.PRRef, sha string) ([]core.CIRun, error)
}

// recordRuns stores the CI runs of the current head and, after a push, of
// the previous head too: its runs may have finished after the last check.
// Failures here don't fail the check; the next check tries again.
func (h *PRCheck) recordRuns(ctx context.Context, pr core.PR, snap core.Snapshot) {
	if h.Runs == nil || snap.HeadSHA == "" {
		return
	}
	shas := []string{snap.HeadSHA}
	if pr.Snapshot != nil && pr.Snapshot.HeadSHA != "" && pr.Snapshot.HeadSHA != snap.HeadSHA {
		shas = append(shas, pr.Snapshot.HeadSHA)
	}
	for _, sha := range shas {
		runs, err := h.Runs.CIRuns(ctx, pr.Key(), sha)
		if err != nil {
			continue
		}
		h.Store.UpsertCIRuns(ctx, pr.ID, runs)
	}
}

func (h *PRCheck) Handle(ctx context.Context, t core.Task) error {
	if t.PRID == nil {
		return errors.New("pr.check without pr_id")
	}
	pr, err := h.Store.GetPR(ctx, *t.PRID)
	if errors.Is(err, core.ErrNotFound) {
		return nil // removed while queued
	}
	if err != nil {
		return err
	}
	now := time.Now()
	snap, err := h.GitHub.Fetch(ctx, pr.Key())
	if err != nil {
		// Show the error on the PR, keep the old snapshot as the baseline, and
		// fail the task so it is visible in `watchcat tasks`. The planner will
		// try again next interval.
		rerr := h.Store.RecordCheck(ctx, pr.ID, core.CheckResult{Status: core.StatusError, Summary: err.Error()}, now, now.Add(h.Interval()))
		return errors.Join(err, rerr)
	}

	h.recordRuns(ctx, pr, snap)
	changes := core.Diff(pr.Snapshot, snap)
	if err := h.Store.RecordCheck(ctx, pr.ID, core.CheckResult{
		Status: snap.Status(), Summary: snap.Summary(), Snapshot: &snap,
	}, now, now.Add(h.Interval())); err != nil {
		return err
	}
	for _, c := range changes {
		if err := h.Store.AddEvent(ctx, pr.ID, c.Kind, c.Message); err != nil {
			return err
		}
		if h.Route == nil {
			continue
		}
		for _, nt := range h.Route(pr, snap, c) {
			if _, err := h.Store.EnqueueTask(ctx, nt); err != nil {
				return err
			}
		}
	}
	if h.Workflows != nil {
		if err := h.Workflows.Evaluate(ctx, pr, snap); err != nil {
			return err
		}
	}
	// Nothing left to watch once a PR is merged or closed.
	if (snap.State == "MERGED" || snap.State == "CLOSED") && !pr.Paused {
		if err := h.Store.SetPaused(ctx, pr.ID, true); err != nil {
			return err
		}
		h.Store.AddEvent(ctx, pr.ID, "stopped", "stopped watching: PR is "+snap.Status())
	}
	return nil
}

// BackfillLifecycle queues one check for PRs whose snapshot predates the
// GitHub timestamps the report needs (opened/merged/closed). Merged PRs are
// paused, so without this they would never be fetched again.
func BackfillLifecycle(ctx context.Context, store *core.Store) (int, error) {
	prs, err := store.ListPRs(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, pr := range prs {
		s := pr.Snapshot
		if s == nil {
			continue
		}
		missing := s.OpenedAt == nil ||
			(s.State == "MERGED" && s.MergedAt == nil) || (s.State == "CLOSED" && s.ClosedAt == nil)
		if !missing {
			continue
		}
		id, err := store.EnqueueTask(ctx, core.NewTask{Kind: KindPRCheck, PRID: pr.ID, DedupeKey: PRCheckDedupe(pr.ID)})
		if err != nil {
			return n, err
		}
		if id != 0 {
			n++
		}
	}
	return n, nil
}
