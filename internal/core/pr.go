// Package core holds the domain model and the SQLite store. It knows nothing
// about HTTP or scheduling, so both the daemon and tests can use it directly.
package core

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PR is one pull request under watch.
//
// Go idiom: exported (capitalised) fields are public; json tags control the
// wire format used by the API.
type PR struct {
	ID          int64      `json:"id"`
	Host        string     `json:"host"` // from the PR URL
	Owner       string     `json:"owner"`
	Repo        string     `json:"repo"`
	Number      int        `json:"number"`
	Paused      bool       `json:"paused"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	CreatedAt   time.Time  `json:"created_at"`
	LastChecked *time.Time `json:"last_checked,omitempty"` // pointer = nullable, like Optional[datetime]
	NextCheck   time.Time  `json:"next_check"`
	Snapshot    *Snapshot  `json:"snapshot,omitempty"` // last state seen on GitHub
	// AgentSession is the AI agent session (e.g. opencode session ID) that
	// owns this PR's changes; workflow prompts go there.
	AgentSession string `json:"agent_session,omitempty"`
	// AgentWorktree is the directory that session works in; opencode
	// commands for the session run there.
	AgentWorktree string `json:"agent_worktree,omitempty"`
}

// Generic statuses; GitHub-derived ones live in snapshot.go.
const (
	StatusUnknown = "unknown"
	StatusOK      = "ok"
	StatusError   = "error"
)

// PRRef identifies a PR on its host. Every gh call takes one, so the host
// can't get lost along the way.
type PRRef struct {
	Host   string `json:"host"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// Slug is HOST/OWNER/REPO, the form `gh -R` accepts for any host.
func (r PRRef) Slug() string { return r.Host + "/" + r.Owner + "/" + r.Repo }

// String is "owner/repo#123", the same on every host.
func (r PRRef) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

func (r PRRef) URL() string {
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", r.Host, r.Owner, r.Repo, r.Number)
}

// Key returns the PR's reference.
func (p PR) Key() PRRef { return PRRef{Host: p.Host, Owner: p.Owner, Repo: p.Repo, Number: p.Number} }

// Ref returns the "owner/repo#123" form.
func (p PR) Ref() string { return p.Key().String() }

func (p PR) URL() string { return p.Key().URL() }

// ErrBadRef is returned for input that isn't a PR URL.
var ErrBadRef = errors.New("expected a PR URL like https://<host>/owner/repo/pull/123")

// ParseRef accepts a PR URL on any host. Only full URLs are accepted: they carry the host, so gh always talks to
// the right one, even when it's signed in to several.
func ParseRef(s string) (PRRef, error) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return PRRef{}, ErrBadRef
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return PRRef{}, ErrBadRef
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return PRRef{}, ErrBadRef
	}
	return PRRef{Host: strings.ToLower(u.Hostname()), Owner: parts[0], Repo: parts[1], Number: n}, nil
}

// Event is an entry in a PR's history (status changes, check runs, errors).
type Event struct {
	ID        int64     `json:"id"`
	PRID      int64     `json:"pr_id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
