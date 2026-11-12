package core

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Report summarises PR lifecycles: how many PRs, how long they took to
// merge, and how often their CI failed.
type Report struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Since       *time.Time `json:"since,omitempty"` // nil = all time

	Watched int `json:"watched"`
	Open    int `json:"open"`
	Merged  int `json:"merged"`
	Closed  int `json:"closed"` // closed without merging
	Paused  int `json:"paused"`

	// TimeToMerge is opened -> merged on GitHub, in seconds, over merged PRs.
	TimeToMerge Stats `json:"time_to_merge"`
	// CIFailures counts CI failures seen per PR while watched, over all PRs
	// in the period.
	CIFailures Stats `json:"ci_failures"`
	// MissingTimes counts merged PRs whose GitHub timestamps aren't known
	// yet (checked before watchdog recorded them; filled in on next check).
	MissingTimes int `json:"missing_times"`

	PRs []ReportRow `json:"prs"`

	// PipelineTime is, per commit, first job start to last job end, in
	// seconds, over commits whose jobs have all finished.
	PipelineTime Stats `json:"pipeline_time"`
	// Jobs has duration stats per CI job name, slowest p95 first.
	Jobs []JobStats `json:"jobs"`
}

// JobStats summarises the runs of one CI job.
type JobStats struct {
	Name     string `json:"name"`
	Runs     int    `json:"runs"`
	Running  int    `json:"running"`
	Failures int    `json:"failures"`
	Duration Stats  `json:"duration"` // seconds, finished runs only
}

// AddCIRuns fills in PipelineTime and Jobs from the runs started in the
// report's period.
func (r *Report) AddCIRuns(runs []CIRun) {
	type commit struct {
		start, end time.Time
		running    bool
	}
	jobs := map[string]*JobStats{}
	durations := map[string][]float64{}
	commits := map[string]*commit{}
	for _, run := range runs {
		j := jobs[run.Name]
		if j == nil {
			j = &JobStats{Name: run.Name}
			jobs[run.Name] = j
		}
		j.Runs++
		key := fmt.Sprintf("%d:%s", run.PRID, run.HeadSHA)
		c := commits[key]
		if c == nil {
			c = &commit{start: run.StartedAt}
			commits[key] = c
		}
		if run.StartedAt.Before(c.start) {
			c.start = run.StartedAt
		}
		if run.CompletedAt == nil {
			j.Running++
			c.running = true
			continue
		}
		if run.Result == ResultSuperseded {
			continue
		}
		if run.CompletedAt.After(c.end) {
			c.end = *run.CompletedAt
		}
		if run.Result != "success" && run.Result != "neutral" && run.Result != "skipped" {
			j.Failures++
		}
		durations[run.Name] = append(durations[run.Name], run.CompletedAt.Sub(run.StartedAt).Seconds())
	}
	var pipeline []float64
	for _, c := range commits {
		if !c.running && !c.end.IsZero() {
			pipeline = append(pipeline, c.end.Sub(c.start).Seconds())
		}
	}
	r.PipelineTime = stats(pipeline)
	r.Jobs = []JobStats{}
	for name, j := range jobs {
		j.Duration = stats(durations[name])
		r.Jobs = append(r.Jobs, *j)
	}
	sort.Slice(r.Jobs, func(a, b int) bool {
		if r.Jobs[a].Duration.P95 != r.Jobs[b].Duration.P95 {
			return r.Jobs[a].Duration.P95 > r.Jobs[b].Duration.P95
		}
		return r.Jobs[a].Name < r.Jobs[b].Name
	})
}

// Stats are nearest-rank percentiles over Count values.
type Stats struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
}

// ReportRow is one PR's lifecycle.
type ReportRow struct {
	ID          int64      `json:"id"`
	Ref         string     `json:"ref"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	State       string     `json:"state"` // open, merged, closed, unknown
	OpenedAt    *time.Time `json:"opened_at,omitempty"`
	MergedAt    *time.Time `json:"merged_at,omitempty"`
	TimeToMerge *float64   `json:"time_to_merge,omitempty"` // seconds
	CIFailures  int        `json:"ci_failures"`
}

// Percentile is the nearest-rank percentile (p in 0..100) of sorted values.
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func stats(values []float64) Stats {
	if len(values) == 0 {
		return Stats{}
	}
	sort.Float64s(values)
	return Stats{Count: len(values), P50: Percentile(values, 50), P95: Percentile(values, 95), Max: values[len(values)-1]}
}

// BuildReport computes the report. A PR counts for the period if it was
// open at any time since `since` (nil = all PRs). failures maps PR id to
// the number of CI failures recorded for it.
func BuildReport(prs []PR, failures map[int64]int, since *time.Time, now time.Time) Report {
	r := Report{GeneratedAt: now, Since: since, PRs: []ReportRow{}}
	var merge, fails []float64
	for _, p := range prs {
		row := ReportRow{ID: p.ID, Ref: p.Ref(), URL: p.URL(), State: "unknown", CIFailures: failures[p.ID]}
		var end *time.Time
		if s := p.Snapshot; s != nil {
			row.Title, row.OpenedAt, row.MergedAt = s.Title, s.OpenedAt, s.MergedAt
			switch s.State {
			case "OPEN":
				row.State = "open"
			case "MERGED":
				row.State, end = "merged", s.MergedAt
			case "CLOSED":
				row.State, end = "closed", s.ClosedAt
			}
		}
		if since != nil && end != nil && end.Before(*since) {
			continue // finished before the period
		}
		r.Watched++
		switch row.State {
		case "open", "unknown":
			r.Open++
		case "merged":
			r.Merged++
		case "closed":
			r.Closed++
		}
		if p.Paused && row.State != "merged" && row.State != "closed" {
			r.Paused++
		}
		if row.State == "merged" {
			if row.OpenedAt != nil && row.MergedAt != nil {
				d := row.MergedAt.Sub(*row.OpenedAt).Seconds()
				row.TimeToMerge = &d
				merge = append(merge, d)
			} else {
				r.MissingTimes++
			}
		}
		fails = append(fails, float64(row.CIFailures))
		r.PRs = append(r.PRs, row)
	}
	r.TimeToMerge, r.CIFailures = stats(merge), stats(fails)
	return r
}

// CIFailureCounts returns how many CI failures were recorded per PR.
func (s *Store) CIFailureCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pr_id, COUNT(*) FROM events WHERE kind = ? GROUP BY pr_id`, ChangeCIFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
