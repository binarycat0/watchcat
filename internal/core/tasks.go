package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Task statuses.
const (
	TaskQueued  = "queued"
	TaskRunning = "running"
	TaskDone    = "done"
	TaskFailed  = "failed"
)

// Task is one unit of background work: a PR check, a Slack message, later a
// review-resolver or agent hand-off. The worker pool runs the handler
// registered for Kind.
type Task struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	PRID        *int64          `json:"pr_id,omitempty"`
	Payload     json.RawMessage `json:"payload"` // handler-specific JSON, decoded by the handler
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	LastError   string          `json:"last_error,omitempty"`
	Result      string          `json:"result,omitempty"` // handler output, e.g. the prompt sent
	RunAt       time.Time       `json:"run_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// NewTask describes a task to enqueue.
type NewTask struct {
	Kind        string
	PRID        int64 // 0 = not tied to a PR
	Payload     any   // marshalled to JSON
	DedupeKey   string
	MaxAttempts int
	RunAt       time.Time // zero = now
}

// EnqueueTask adds a task. If DedupeKey matches a task that is still queued or
// running, nothing is added and (0, nil) is returned.
func (s *Store) EnqueueTask(ctx context.Context, t NewTask) (int64, error) {
	payload, err := json.Marshal(t.Payload)
	if err != nil {
		return 0, err
	}
	if t.Payload == nil {
		payload = []byte("{}")
	}
	if t.MaxAttempts <= 0 {
		t.MaxAttempts = 1
	}
	now := time.Now()
	if t.RunAt.IsZero() {
		t.RunAt = now
	}
	var prID, dedupe any
	if t.PRID != 0 {
		prID = t.PRID
	}
	if t.DedupeKey != "" {
		dedupe = t.DedupeKey
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO tasks (kind, pr_id, payload, dedupe_key, max_attempts, run_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Kind, prID, string(payload), dedupe, t.MaxAttempts, t.RunAt.Unix(), now.Unix(), now.Unix())
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, nil
	}
	return res.LastInsertId()
}

const taskCols = `id, kind, pr_id, payload, status, attempts, max_attempts, last_error, result, run_at, created_at, updated_at`

func scanTask(sc scanner) (Task, error) {
	var t Task
	var prID sql.NullInt64
	var payload string
	var runAt, created, updated int64
	err := sc.Scan(&t.ID, &t.Kind, &prID, &payload, &t.Status, &t.Attempts, &t.MaxAttempts, &t.LastError, &t.Result, &runAt, &created, &updated)
	if err != nil {
		return t, err
	}
	if prID.Valid {
		t.PRID = &prID.Int64
	}
	t.Payload = json.RawMessage(payload)
	t.RunAt, t.CreatedAt, t.UpdatedAt = time.Unix(runAt, 0), time.Unix(created, 0), time.Unix(updated, 0)
	return t, nil
}

// ClaimTask atomically marks the oldest ready task as running and returns it.
// ok is false when nothing is ready.
func (s *Store) ClaimTask(ctx context.Context, now time.Time) (t Task, ok bool, err error) {
	t, err = scanTask(s.db.QueryRowContext(ctx,
		`UPDATE tasks SET status = 'running', attempts = attempts + 1, updated_at = ?
		 WHERE id = (SELECT id FROM tasks WHERE status = 'queued' AND run_at <= ? ORDER BY run_at, id LIMIT 1)
		 RETURNING `+taskCols, now.Unix(), now.Unix()))
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

// CompleteTask marks a task done.
func (s *Store) CompleteTask(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'done', last_error = '', updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// FailTask records an error. The task is retried at retryAt if it has
// attempts left, otherwise it is marked failed for good.
func (s *Store) FailTask(ctx context.Context, t Task, cause error, retryAt time.Time) error {
	status, runAt := TaskFailed, t.RunAt
	if t.Attempts < t.MaxAttempts {
		status, runAt = TaskQueued, retryAt
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, last_error = ?, run_at = ?, updated_at = ? WHERE id = ?`,
		status, cause.Error(), runAt.Unix(), time.Now().Unix(), t.ID)
	return err
}

// SetTaskResult stores a handler's output so it shows up in the task list.
func (s *Store) SetTaskResult(ctx context.Context, id int64, result string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET result = ? WHERE id = ?`, result, id)
	return err
}

// RequeueRunning puts tasks left "running" by a crashed or killed daemon back
// in the queue. Call once at startup, before workers start.
func (s *Store) RequeueRunning(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'queued', updated_at = ? WHERE status = 'running'`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneTasks deletes finished tasks older than the cutoff.
func (s *Store) PruneTasks(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM tasks WHERE status IN ('done', 'failed') AND updated_at < ?`, before.Unix())
	return err
}

// TaskFilter narrows ListTasks. Zero values mean "any".
type TaskFilter struct {
	Kind   string
	Status string
	PRID   int64
	Limit  int
	Offset int // rows to skip, for paging
}

// ListTasks returns tasks, newest first.
func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	var args []any
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.PRID != 0 {
		q += ` AND pr_id = ?`
		args = append(args, f.PRID)
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	q += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
