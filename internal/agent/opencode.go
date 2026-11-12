// Package agent hands work to AI agent sessions. Today that is opencode,
// driven through its CLI (`opencode api <method> <endpoint>`) the same way
// the earlier Python pr-watchdog did: no server to run, and each call runs
// from the session's worktree so opencode resolves the right project.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Session is an opencode session as watchcat needs it.
type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"` // the worktree it works in
	Archived  bool   `json:"archived"`
}

// Runner executes `opencode` with args in dir and returns stdout. A field so
// tests can fake opencode, which isn't installed everywhere.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// Opencode talks to opencode through its CLI.
type Opencode struct {
	Run Runner // nil = the real `opencode` binary
}

// ExecRunner runs the real opencode CLI.
func ExecRunner(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			return nil, errors.New("opencode is not installed or not on PATH")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("opencode api: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("opencode api: %w", err)
	}
	return out, nil
}

var sessionIDRe = regexp.MustCompile(`^ses_[A-Za-z0-9]+$`)

// ValidID reports whether s looks like an opencode session ID.
func ValidID(s string) bool { return sessionIDRe.MatchString(s) }

// api calls `opencode api <method> <endpoint> [--data <json>]` in dir and
// returns the response with opencode's {"data": ...} envelope removed.
func (o Opencode) api(ctx context.Context, dir, method, endpoint string, body any) (json.RawMessage, error) {
	run := o.Run
	if run == nil {
		run = ExecRunner
	}
	args := []string{"api", method, endpoint}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		args = append(args, "--data", string(b))
	}
	out, err := run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// unwrap returns v["data"] when the response is an object with a data key.
func unwrap(raw json.RawMessage) json.RawMessage {
	var env map[string]json.RawMessage
	if json.Unmarshal(raw, &env) == nil {
		if d, ok := env["data"]; ok {
			return d
		}
	}
	return raw
}

// apiSession is the wire shape; "archived" is a timestamp, 0 or absent when not.
type apiSession struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Time struct {
		Archived json.Number `json:"archived"`
	} `json:"time"`
}

func (a apiSession) session() Session {
	archived := a.Time.Archived != "" && a.Time.Archived != "0"
	return Session{ID: a.ID, Title: a.Title, Directory: a.Location.Directory, Archived: archived}
}

// Sessions lists unarchived sessions, following opencode's cursor pages.
func (o Opencode) Sessions(ctx context.Context, dir string) ([]Session, error) {
	q := url.Values{"archived": {"false"}, "limit": {"1000"}}
	var out []Session
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		raw, err := o.api(ctx, dir, "get", "/api/session?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data   []apiSession `json:"data"`
			Cursor struct {
				Next *string `json:"next"`
			} `json:"cursor"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("unexpected session list from opencode: %w", err)
		}
		for _, s := range resp.Data {
			if s.ID != "" {
				out = append(out, s.session())
			}
		}
		next := resp.Cursor.Next
		if next == nil || *next == "" || len(resp.Data) == 0 {
			return out, nil
		}
		if seen[*next] {
			return nil, errors.New("opencode session list repeats a cursor")
		}
		seen[*next] = true
		q.Set("cursor", *next)
	}
	return nil, errors.New("opencode session list: too many pages")
}

// Session fetches one session, running from dir.
func (o Opencode) Session(ctx context.Context, dir, id string) (Session, error) {
	if !ValidID(id) {
		return Session{}, fmt.Errorf("%q is not an opencode session ID (ses_...)", id)
	}
	raw, err := o.api(ctx, dir, "get", "/api/session/"+id, nil)
	if err != nil {
		return Session{}, fmt.Errorf("session %s not found or opencode unavailable: %w", id, err)
	}
	var s apiSession
	if err := json.Unmarshal(unwrap(raw), &s); err != nil || s.ID == "" {
		return Session{}, fmt.Errorf("session %s not found", id)
	}
	return s.session(), nil
}

// Resolve checks a session is usable for a PR: it exists, isn't archived,
// and works in worktree. An empty worktree is taken from the session. It
// returns the session with its (cleaned) directory.
func (o Opencode) Resolve(ctx context.Context, id, worktree string) (Session, error) {
	dir := worktree
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home
	} else if !filepath.IsAbs(dir) {
		return Session{}, fmt.Errorf("worktree must be an absolute path, got %q", worktree)
	} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Session{}, fmt.Errorf("worktree %s is not a directory", worktree)
	}
	s, err := o.Session(ctx, dir, id)
	if err != nil {
		return Session{}, err
	}
	if s.Archived {
		return Session{}, fmt.Errorf("session %s is archived", id)
	}
	if s.Directory == "" {
		return Session{}, fmt.Errorf("session %s has no working directory", id)
	}
	if worktree != "" && filepath.Clean(s.Directory) != filepath.Clean(worktree) {
		return Session{}, fmt.Errorf("session %s works in %s, not %s", id, s.Directory, worktree)
	}
	s.Directory = filepath.Clean(s.Directory)
	return s, nil
}

// Prompt queues text in the session (delivered when it's idle, resuming it
// if needed) and returns opencode's inbox ID for the message. It re-checks
// the session first so a prompt never lands in a moved or archived one.
func (o Opencode) Prompt(ctx context.Context, id, worktree, text, key string) (string, error) {
	if _, err := o.Resolve(ctx, id, worktree); err != nil {
		return "", err
	}
	raw, err := o.api(ctx, worktree, "post", "/api/session/"+id+"/prompt", map[string]any{
		"text": text, "delivery": "queue", "resume": true,
		"metadata": map[string]string{"watchcat_task": key},
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(unwrap(raw), &resp) != nil || resp.ID == "" {
		return "", errors.New("opencode accepted the request but returned no inbox ID; check the session before retrying")
	}
	return resp.ID, nil
}
