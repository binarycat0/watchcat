// Package core holds the domain model and the SQLite store. It knows nothing
// about HTTP or scheduling, so both the daemon and tests can use it directly.
package core

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
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
	Host        string     `json:"host"` // github.com or a GitHub Enterprise host
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

// DefaultHost is used when a ref doesn't name one.
const DefaultHost = "github.com"

// PRRef identifies a PR on a GitHub host. Every gh call takes one, so the
// host (github.com or GitHub Enterprise) can't get lost along the way.
type PRRef struct {
	Host   string `json:"host"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// Slug is HOST/OWNER/REPO, the form `gh -R` accepts for any host.
func (r PRRef) Slug() string { return r.Host + "/" + r.Owner + "/" + r.Repo }

// String is "owner/repo#123" on github.com, "host/owner/repo#123" elsewhere.
func (r PRRef) String() string {
	s := fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
	if r.Host != DefaultHost {
		s = r.Host + "/" + s
	}
	return s
}

func (r PRRef) URL() string {
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", r.Host, r.Owner, r.Repo, r.Number)
}

// Key returns the PR's reference.
func (p PR) Key() PRRef { return PRRef{Host: p.Host, Owner: p.Owner, Repo: p.Repo, Number: p.Number} }

// Ref returns the canonical "owner/repo#123" (or "host/owner/repo#123") form.
func (p PR) Ref() string { return p.Key().String() }

func (p PR) URL() string { return p.Key().URL() }

// owner/repo#N, or host/owner/repo#N where host contains a dot.
var refRe = regexp.MustCompile(`^(?:([\w-]+(?:\.[\w-]+)+)/)?([\w.-]+)/([\w.-]+)#(\d+)$`)

// ErrBadRef is returned for input that is neither a short ref nor a PR URL.
var ErrBadRef = errors.New("expected owner/repo#123, host/owner/repo#123 or https://<host>/owner/repo/pull/123")

// ParseRef accepts "owner/repo#123" (on defaultHost), "host/owner/repo#123",
// or a PR URL on any host (github.com or GitHub Enterprise).
// Go returns (value, error) pairs instead of raising exceptions.
func ParseRef(s, defaultHost string) (PRRef, error) {
	s = strings.TrimSpace(s)
	if defaultHost == "" {
		defaultHost = DefaultHost
	}
	if m := refRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[4])
		host := m[1]
		if host == "" {
			host = defaultHost
		}
		return PRRef{Host: strings.ToLower(host), Owner: m[2], Repo: m[3], Number: n}, nil
	}
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
