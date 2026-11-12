package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/binarycat0/watchdog/internal/core"
	"github.com/binarycat0/watchdog/internal/github"
)

const KindPRRetrigger = "pr.retrigger"

// CIRetrigger is what Retrigger needs from GitHub; github.GH implements it.
type CIRetrigger interface {
	RerunFailed(ctx context.Context, ref core.PRRef, runID string) error
}

// Retrigger re-runs the failed GitHub Actions jobs of a PR with
// `gh run rerun --failed`. Checks from other CI systems are reported as
// skipped, since GitHub can't re-run them.
type Retrigger struct {
	Store        *core.Store
	GitHub       CIRetrigger
	RecheckAfter time.Duration // when to look at the PR again
}

func (h *Retrigger) Handle(ctx context.Context, t core.Task) error {
	if t.PRID == nil {
		return errors.New("pr.retrigger without pr_id")
	}
	pr, err := h.Store.GetPR(ctx, *t.PRID)
	if err != nil {
		return err
	}
	if pr.Snapshot == nil || len(pr.Snapshot.FailedChecks) == 0 {
		return errors.New("no failed checks recorded; run a check first")
	}

	runs := map[string]bool{}
	var external []string
	for _, c := range pr.Snapshot.FailedChecks {
		if id := github.ActionsRunID(c.URL); id != "" {
			runs[id] = true
		} else {
			external = append(external, c.Name)
		}
	}

	var done []string
	var errs []error
	for id := range runs {
		if err := h.GitHub.RerunFailed(ctx, pr.Key(), id); err != nil {
			errs = append(errs, err)
			continue
		}
		done = append(done, "Actions run "+id)
	}
	if len(external) > 0 {
		errs = append(errs, fmt.Errorf("skipped %s: not GitHub Actions checks", strings.Join(external, ", ")))
	}

	if len(done) > 0 {
		h.Store.AddEvent(ctx, pr.ID, "retrigger", "re-ran "+strings.Join(done, "; "))
		// Look again once CI has had time to pick the jobs up.
		h.Store.EnqueueTask(ctx, core.NewTask{
			Kind: KindPRCheck, PRID: pr.ID, DedupeKey: PRCheckDedupe(pr.ID),
			RunAt: time.Now().Add(h.RecheckAfter),
		})
	}
	return errors.Join(errs...)
}
