package github

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/binarycat0/watchdog/internal/core"
)

// CIRuns returns every CI job run on a commit: GitHub Actions check runs
// (exact start/end per job; a rerun is a new check run) and commit statuses
// from external CI such as Jenkins, grouped into runs from their history.
func (g GH) CIRuns(ctx context.Context, ref core.PRRef, sha string) ([]core.CIRun, error) {
	repo := ref.Owner + "/" + ref.Repo
	checks, err := g.ndjson(ctx, ref.Host, "repos/"+repo+"/commits/"+sha+"/check-runs?per_page=100",
		".check_runs[] | {id, name, status, conclusion, started_at, completed_at, html_url}")
	if err != nil {
		return nil, err
	}
	statuses, err := g.ndjson(ctx, ref.Host, "repos/"+repo+"/commits/"+sha+"/statuses?per_page=100",
		".[] | {id, context, state, created_at, target_url}")
	if err != nil {
		return nil, err
	}
	runs, err := ParseCheckRuns(checks, sha)
	if err != nil {
		return nil, err
	}
	more, err := ParseStatuses(statuses, sha)
	if err != nil {
		return nil, err
	}
	return append(runs, more...), nil
}

// ndjson runs a paginated `gh api` call whose --jq emits one object per line.
func (g GH) ndjson(ctx context.Context, host, path, jq string) ([]byte, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	out, err := exec.CommandContext(ctx, bin, "api", "--hostname", host, "--paginate", path, "--jq", jq).Output()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w", path, err)
	}
	return out, nil
}

func eachLine(b []byte, f func([]byte) error) error {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			if err := f(line); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// ParseCheckRuns turns check-run lines into runs. Queued runs without a
// start time are skipped until they start.
func ParseCheckRuns(b []byte, sha string) ([]core.CIRun, error) {
	var out []core.CIRun
	err := eachLine(b, func(line []byte) error {
		var c struct {
			ID          int64      `json:"id"`
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
			URL         string     `json:"html_url"`
		}
		if err := json.Unmarshal(line, &c); err != nil {
			return fmt.Errorf("check run: %w", err)
		}
		if c.StartedAt == nil {
			return nil
		}
		r := core.CIRun{HeadSHA: sha, Name: c.Name, Kind: "actions", RunKey: fmt.Sprintf("check:%d", c.ID),
			URL: c.URL, StartedAt: *c.StartedAt}
		if c.Status == "completed" {
			r.Result, r.CompletedAt = c.Conclusion, c.CompletedAt
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

// ParseStatuses groups a commit's status history into runs. External CI
// posts "pending" when a build starts (often several times) and a final
// state when it ends; a run is the first status of a context up to its
// first final state. A new build (different target URL) or any status after
// a final one starts a new run, which is how reruns show up.
func ParseStatuses(b []byte, sha string) ([]core.CIRun, error) {
	type status struct {
		ID        int64     `json:"id"`
		Context   string    `json:"context"`
		State     string    `json:"state"`
		CreatedAt time.Time `json:"created_at"`
		URL       string    `json:"target_url"`
	}
	var all []status
	if err := eachLine(b, func(line []byte) error {
		var s status
		if err := json.Unmarshal(line, &s); err != nil {
			return fmt.Errorf("status: %w", err)
		}
		all = append(all, s)
		return nil
	}); err != nil {
		return nil, err
	}
	// The API returns newest first; walk each context oldest first.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID < all[j].ID
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	open := map[string]*core.CIRun{} // context -> run in progress
	var out []*core.CIRun
	for _, s := range all {
		cur := open[s.Context]
		if cur != nil && cur.URL != "" && s.URL != "" && s.URL != cur.URL {
			// A new build replaced one that never reported a result: close
			// it as superseded so it doesn't look like it's still running.
			t := s.CreatedAt
			cur.Result, cur.CompletedAt = core.ResultSuperseded, &t
			cur = nil
		}
		if cur == nil {
			cur = &core.CIRun{HeadSHA: sha, Name: s.Context, Kind: "status",
				RunKey: fmt.Sprintf("status:%s:%d", s.Context, s.ID), StartedAt: s.CreatedAt}
			out = append(out, cur)
			open[s.Context] = cur
		}
		if s.URL != "" {
			cur.URL = s.URL
		}
		if s.State != "pending" {
			t := s.CreatedAt
			cur.Result, cur.CompletedAt = s.State, &t
			delete(open, s.Context)
		}
	}
	runs := make([]core.CIRun, len(out))
	for i, r := range out {
		runs[i] = *r
	}
	return runs, nil
}
