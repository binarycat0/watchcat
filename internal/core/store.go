package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver; the blank import runs its init()
)

// ErrNotFound is returned when a PR does not exist.
var ErrNotFound = errors.New("not found")

// ErrExists is returned when adding a PR that is already watched.
var ErrExists = errors.New("already watched")

// Store wraps the SQLite database. Only the daemon process opens it for
// writing; the CLI goes through the API.
type Store struct {
	db *sql.DB
}

// migrations run in order; the index+1 is stored in PRAGMA user_version.
// Append new entries, never edit old ones.
var migrations = []string{
	`CREATE TABLE prs (
		id           INTEGER PRIMARY KEY,
		owner        TEXT NOT NULL,
		repo         TEXT NOT NULL,
		number       INTEGER NOT NULL,
		paused       INTEGER NOT NULL DEFAULT 0,
		status       TEXT NOT NULL DEFAULT 'unknown',
		summary      TEXT NOT NULL DEFAULT '',
		created_at   INTEGER NOT NULL,
		last_checked INTEGER,
		next_check   INTEGER NOT NULL,
		UNIQUE (owner, repo, number)
	);
	CREATE TABLE events (
		id         INTEGER PRIMARY KEY,
		pr_id      INTEGER NOT NULL REFERENCES prs(id) ON DELETE CASCADE,
		kind       TEXT NOT NULL,
		message    TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX events_pr ON events(pr_id, created_at);`,

	// 2: durable task queue + last GitHub snapshot per PR for diffing.
	`CREATE TABLE tasks (
		id           INTEGER PRIMARY KEY,
		kind         TEXT NOT NULL,
		pr_id        INTEGER REFERENCES prs(id) ON DELETE CASCADE,
		payload      TEXT NOT NULL DEFAULT '{}',
		status       TEXT NOT NULL DEFAULT 'queued',
		dedupe_key   TEXT,
		attempts     INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 1,
		last_error   TEXT NOT NULL DEFAULT '',
		run_at       INTEGER NOT NULL,
		created_at   INTEGER NOT NULL,
		updated_at   INTEGER NOT NULL
	);
	CREATE INDEX tasks_ready ON tasks(status, run_at);
	CREATE UNIQUE INDEX tasks_dedupe ON tasks(dedupe_key) WHERE status IN ('queued', 'running');
	ALTER TABLE prs ADD COLUMN snapshot TEXT NOT NULL DEFAULT '';`,

	// 3: workflow rules, task output, agent session binding.
	`CREATE TABLE rule_runs (
		pr_id      INTEGER NOT NULL REFERENCES prs(id) ON DELETE CASCADE,
		rule       TEXT NOT NULL,
		state_key  TEXT NOT NULL,
		mode       TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (pr_id, rule, state_key)
	);
	ALTER TABLE tasks ADD COLUMN result TEXT NOT NULL DEFAULT '';
	ALTER TABLE prs ADD COLUMN agent_session TEXT NOT NULL DEFAULT '';`,

	// 4: PRs get a host, and the unique key includes it,
	// which in SQLite means rebuilding the table (see migrate for why this
	// doesn't cascade-delete history).
	`CREATE TABLE prs_new (
		id            INTEGER PRIMARY KEY,
		host          TEXT NOT NULL DEFAULT 'github.com',
		owner         TEXT NOT NULL,
		repo          TEXT NOT NULL,
		number        INTEGER NOT NULL,
		paused        INTEGER NOT NULL DEFAULT 0,
		status        TEXT NOT NULL DEFAULT 'unknown',
		summary       TEXT NOT NULL DEFAULT '',
		created_at    INTEGER NOT NULL,
		last_checked  INTEGER,
		next_check    INTEGER NOT NULL,
		snapshot      TEXT NOT NULL DEFAULT '',
		agent_session TEXT NOT NULL DEFAULT '',
		UNIQUE (host, owner, repo, number)
	);
	INSERT INTO prs_new (id, owner, repo, number, paused, status, summary, created_at, last_checked, next_check, snapshot, agent_session)
		SELECT id, owner, repo, number, paused, status, summary, created_at, last_checked, next_check, snapshot, agent_session FROM prs;
	DROP TABLE prs;
	ALTER TABLE prs_new RENAME TO prs;`,

	// 5: an agent binding is a session plus the worktree it runs in.
	`ALTER TABLE prs ADD COLUMN agent_worktree TEXT NOT NULL DEFAULT '';`,

	// 6: every CI job run per PR, for duration history.
	`CREATE TABLE ci_runs (
		id           INTEGER PRIMARY KEY,
		pr_id        INTEGER NOT NULL REFERENCES prs(id) ON DELETE CASCADE,
		head_sha     TEXT NOT NULL,
		name         TEXT NOT NULL,
		kind         TEXT NOT NULL,
		run_key      TEXT NOT NULL,
		url          TEXT NOT NULL DEFAULT '',
		result       TEXT NOT NULL DEFAULT '',
		started_at   INTEGER NOT NULL,
		completed_at INTEGER,
		UNIQUE (pr_id, run_key)
	);
	CREATE INDEX ci_runs_started ON ci_runs(started_at);`,
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	// WAL lets readers proceed while the worker writes; busy_timeout waits
	// instead of failing on a locked database.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a single connection avoids
	// SQLITE_BUSY between our own goroutines.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == len(migrations) {
		return nil
	}
	// Table rebuilds (CREATE new, copy, DROP old, RENAME) would cascade-delete
	// rows that reference the old table, so foreign keys are off while
	// migrating and checked afterwards. This is SQLite's documented procedure;
	// the pragma only works outside a transaction.
	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer s.db.Exec(`PRAGMA foreign_keys = ON`)
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		// PRAGMA does not accept bound parameters, hence Sprintf.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("migration left dangling foreign keys")
	}
	return rows.Err()
}

const prCols = `id, host, owner, repo, number, paused, status, summary, created_at, last_checked, next_check, snapshot, agent_session, agent_worktree`

// scanner is satisfied by both *sql.Row and *sql.Rows. Go interfaces are
// implicit: any type with a matching Scan method fits, no "implements" needed.
type scanner interface{ Scan(dest ...any) error }

func scanPR(sc scanner) (PR, error) {
	var p PR
	var created, next int64
	var last sql.NullInt64
	var snap string
	err := sc.Scan(&p.ID, &p.Host, &p.Owner, &p.Repo, &p.Number, &p.Paused, &p.Status, &p.Summary, &created, &last, &next, &snap, &p.AgentSession, &p.AgentWorktree)
	if err != nil {
		return p, err
	}
	if snap != "" {
		p.Snapshot = &Snapshot{}
		if err := json.Unmarshal([]byte(snap), p.Snapshot); err != nil {
			return p, fmt.Errorf("PR %d snapshot: %w", p.ID, err)
		}
	}
	p.CreatedAt = time.Unix(created, 0)
	p.NextCheck = time.Unix(next, 0)
	if last.Valid {
		t := time.Unix(last.Int64, 0)
		p.LastChecked = &t
	}
	return p, nil
}

// AddPR starts watching a PR. It is due for a check immediately.
func (s *Store) AddPR(ctx context.Context, ref PRRef) (PR, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO prs (host, owner, repo, number, created_at, next_check) VALUES (?, ?, ?, ?, ?, ?)`,
		ref.Host, ref.Owner, ref.Repo, ref.Number, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return PR{}, ErrExists
		}
		return PR{}, err
	}
	id, _ := res.LastInsertId()
	s.AddEvent(ctx, id, "added", "started watching")
	return s.GetPR(ctx, id)
}

func (s *Store) GetPR(ctx context.Context, id int64) (PR, error) {
	p, err := scanPR(s.db.QueryRowContext(ctx, `SELECT `+prCols+` FROM prs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// FindPR looks a PR up by its reference.
func (s *Store) FindPR(ctx context.Context, ref PRRef) (PR, error) {
	p, err := scanPR(s.db.QueryRowContext(ctx,
		`SELECT `+prCols+` FROM prs WHERE host = ? AND owner = ? AND repo = ? AND number = ?`, ref.Host, ref.Owner, ref.Repo, ref.Number))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ListPRs(ctx context.Context) ([]PR, error) {
	return s.queryPRs(ctx, `SELECT `+prCols+` FROM prs ORDER BY host, owner, repo, number`)
}

// DuePRs returns unpaused PRs whose next_check is at or before now.
func (s *Store) DuePRs(ctx context.Context, now time.Time) ([]PR, error) {
	return s.queryPRs(ctx, `SELECT `+prCols+` FROM prs WHERE paused = 0 AND next_check <= ? ORDER BY next_check`, now.Unix())
}

func (s *Store) queryPRs(ctx context.Context, q string, args ...any) ([]PR, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // defer runs when the function returns, like a finally block
	prs := []PR{}      // non-nil so the API encodes [] rather than null
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			return nil, err
		}
		prs = append(prs, p)
	}
	return prs, rows.Err()
}

func (s *Store) RemovePR(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM prs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetPaused(ctx context.Context, id int64, paused bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE prs SET paused = ? WHERE id = ?`, paused, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAgentSession links a PR to the agent session that owns it and the
// worktree that session works in ("" for both unlinks).
func (s *Store) SetAgentSession(ctx context.Context, id int64, session, worktree string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE prs SET agent_session = ?, agent_worktree = ? WHERE id = ?`, session, worktree, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkRuleFired records that a workflow rule fired for a PR in a given state.
// It returns false if the rule already fired for that state, which is how
// rules run once per state instead of on every check.
func (s *Store) MarkRuleFired(ctx context.Context, prID int64, rule, stateKey, mode string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO rule_runs (pr_id, rule, state_key, mode, created_at) VALUES (?, ?, ?, ?, ?)`,
		prID, rule, stateKey, mode, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RuleRun is one recorded firing of a workflow rule.
type RuleRun struct {
	Rule      string    `json:"rule"`
	StateKey  string    `json:"state_key"`
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"created_at"`
}

// LastRuleRuns returns the most recent firing of each rule for a PR.
func (s *Store) LastRuleRuns(ctx context.Context, prID int64) (map[string]RuleRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT rule, state_key, mode, created_at FROM rule_runs WHERE pr_id = ? ORDER BY created_at`, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]RuleRun{}
	for rows.Next() {
		var r RuleRun
		var ts int64
		if err := rows.Scan(&r.Rule, &r.StateKey, &r.Mode, &ts); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(ts, 0)
		out[r.Rule] = r // ordered by time, so the last one wins
	}
	return out, rows.Err()
}

// SetNextCheck reschedules a PR's next check.
func (s *Store) SetNextCheck(ctx context.Context, id int64, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE prs SET next_check = ? WHERE id = ?`, next.Unix(), id)
	return err
}

// CheckResult is the outcome of one PR check.
type CheckResult struct {
	Status   string
	Summary  string
	Snapshot *Snapshot // nil when the check failed before reaching GitHub
}

// RecordCheck stores the result of a check and schedules the next one. A
// status change is also written to the PR's history. The previous snapshot is
// kept when the check produced none, so the next diff still has a baseline.
func (s *Store) RecordCheck(ctx context.Context, id int64, r CheckResult, checkedAt, next time.Time) error {
	prev, err := s.GetPR(ctx, id)
	if err != nil {
		return err
	}
	q := `UPDATE prs SET status = ?, summary = ?, last_checked = ?, next_check = ? WHERE id = ?`
	args := []any{r.Status, r.Summary, checkedAt.Unix(), next.Unix(), id}
	if r.Snapshot != nil {
		b, err := json.Marshal(r.Snapshot)
		if err != nil {
			return err
		}
		q = `UPDATE prs SET status = ?, summary = ?, last_checked = ?, next_check = ?, snapshot = ? WHERE id = ?`
		args = []any{r.Status, r.Summary, checkedAt.Unix(), next.Unix(), string(b), id}
	}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	if prev.Status != r.Status {
		msg := fmt.Sprintf("%s -> %s", prev.Status, r.Status)
		if r.Summary != "" {
			msg += ": " + r.Summary
		}
		return s.AddEvent(ctx, id, "status", msg)
	}
	return nil
}

func (s *Store) AddEvent(ctx context.Context, prID int64, kind, message string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events (pr_id, kind, message, created_at) VALUES (?, ?, ?, ?)`,
		prID, kind, message, time.Now().Unix())
	return err
}

// Events returns a PR's history, newest first.
func (s *Store) Events(ctx context.Context, prID int64, limit, offset int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, pr_id, kind, message, created_at FROM events WHERE pr_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		prID, limit, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evs := []Event{}
	for rows.Next() {
		var e Event
		var ts int64
		if err := rows.Scan(&e.ID, &e.PRID, &e.Kind, &e.Message, &ts); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(ts, 0)
		evs = append(evs, e)
	}
	return evs, rows.Err()
}
