package core

import (
	"context"
	"database/sql"
	"time"
)

// CIRun is one execution of one CI job on one commit. A rerun or retrigger
// is a new CIRun, so a PR keeps the full history of its pipeline runs.
type CIRun struct {
	ID          int64      `json:"id"`
	PRID        int64      `json:"pr_id"`
	HeadSHA     string     `json:"head_sha"`
	Name        string     `json:"name"`    // job / status context name
	Kind        string     `json:"kind"`    // "actions" (check run) or "status" (e.g. Jenkins)
	RunKey      string     `json:"run_key"` // stable identity of this run, unique per PR
	URL         string     `json:"url,omitempty"`
	Result      string     `json:"result"` // success, failure, error, cancelled, ...; "" while running
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ResultSuperseded marks a run replaced by a newer build before it reported
// a result. It is left out of duration and failure stats.
const ResultSuperseded = "superseded"

// Duration is how long the run took, or has been running so far.
func (r CIRun) Duration(now time.Time) time.Duration {
	if r.CompletedAt != nil {
		return r.CompletedAt.Sub(r.StartedAt)
	}
	return now.Sub(r.StartedAt)
}

// UpsertCIRuns records runs seen on a check. A run already stored (same PR
// and run key) gets its result and completion updated, e.g. when a running
// job has finished since the last check.
func (s *Store) UpsertCIRuns(ctx context.Context, prID int64, runs []CIRun) error {
	for _, r := range runs {
		var done sql.NullInt64
		if r.CompletedAt != nil {
			done = sql.NullInt64{Int64: r.CompletedAt.Unix(), Valid: true}
		}
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO ci_runs (pr_id, head_sha, name, kind, run_key, url, result, started_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (pr_id, run_key) DO UPDATE SET
				url = excluded.url, result = excluded.result, completed_at = excluded.completed_at`,
			prID, r.HeadSHA, r.Name, r.Kind, r.RunKey, r.URL, r.Result, r.StartedAt.Unix(), done)
		if err != nil {
			return err
		}
	}
	return nil
}

const ciRunCols = `id, pr_id, head_sha, name, kind, run_key, url, result, started_at, completed_at`

func (s *Store) queryCIRuns(ctx context.Context, q string, args ...any) ([]CIRun, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CIRun{}
	for rows.Next() {
		var r CIRun
		var started int64
		var done sql.NullInt64
		if err := rows.Scan(&r.ID, &r.PRID, &r.HeadSHA, &r.Name, &r.Kind, &r.RunKey, &r.URL, &r.Result, &started, &done); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0)
		if done.Valid {
			t := time.Unix(done.Int64, 0)
			r.CompletedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CIRunsForPR returns a PR's run history, newest first.
func (s *Store) CIRunsForPR(ctx context.Context, prID int64) ([]CIRun, error) {
	return s.queryCIRuns(ctx, `SELECT `+ciRunCols+` FROM ci_runs WHERE pr_id = ? ORDER BY started_at DESC, id DESC`, prID)
}

// CIRunsSince returns all runs started at or after since (zero = all).
func (s *Store) CIRunsSince(ctx context.Context, since time.Time) ([]CIRun, error) {
	return s.queryCIRuns(ctx, `SELECT `+ciRunCols+` FROM ci_runs WHERE started_at >= ? ORDER BY started_at`, since.Unix())
}
