package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/binarycat0/watchcat/internal/agent"
	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/health"
)

// ErrDaemonDown means nothing is listening on the API address.
var ErrDaemonDown = errors.New("daemon not running")

// Client talks to a running daemon. The CLI and any future tool use this.
type Client struct {
	Base string // e.g. http://127.0.0.1:7878
	HTTP *http.Client
}

func NewClient(listen string) *Client {
	return &Client{Base: "http://" + listen, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// APIError is a non-2xx response with the server's message.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string { return e.Message }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrDaemonDown
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e errorResponse
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &APIError{Code: resp.StatusCode, Message: e.Error}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var s StatusResponse
	return s, c.do(ctx, "GET", "/api/v1/status", nil, &s)
}

func (c *Client) ListPRs(ctx context.Context) ([]core.PR, error) {
	var prs []core.PR
	return prs, c.do(ctx, "GET", "/api/v1/prs", nil, &prs)
}

func (c *Client) AddPR(ctx context.Context, ref string) (core.PR, error) {
	var pr core.PR
	return pr, c.do(ctx, "POST", "/api/v1/prs", addRequest{Ref: ref}, &pr)
}

func (c *Client) GetPR(ctx context.Context, id int64) (PRDetail, error) {
	var d PRDetail
	return d, c.do(ctx, "GET", fmt.Sprintf("/api/v1/prs/%d", id), nil, &d)
}

func (c *Client) RemovePR(ctx context.Context, id int64) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/v1/prs/%d", id), nil, nil)
}

func (c *Client) CheckNow(ctx context.Context, id int64) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/v1/prs/%d/check", id), nil, nil)
}

// BindAgent links a PR to an agent session and its worktree ("" = take it
// from the session); an empty session unlinks.
func (c *Client) BindAgent(ctx context.Context, id int64, session, worktree string) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/v1/prs/%d/agent", id),
		map[string]string{"session": session, "worktree": worktree}, nil)
}

// PromptAgent queues a ci_failed or review_threads prompt to the PR's
// session. It reports dryRun when the matching rule isn't "on", in which
// case the prompt is only built and recorded.
func (c *Client) PromptAgent(ctx context.Context, id int64, prompt string) (dryRun bool, err error) {
	var resp struct {
		DryRun bool `json:"dry_run"`
	}
	err = c.do(ctx, "POST", fmt.Sprintf("/api/v1/prs/%d/agent/prompt", id), map[string]string{"prompt": prompt}, &resp)
	return resp.DryRun, err
}

// CIRuns returns a PR's CI run history, newest first.
func (c *Client) CIRuns(ctx context.Context, id int64) ([]core.CIRun, error) {
	var runs []core.CIRun
	return runs, c.do(ctx, "GET", fmt.Sprintf("/api/v1/prs/%d/ci-runs", id), nil, &runs)
}

// AgentSessions lists the agent's (opencode's) unarchived sessions.
func (c *Client) AgentSessions(ctx context.Context) ([]agent.Session, error) {
	var ss []agent.Session
	return ss, c.do(ctx, "GET", "/api/v1/agent/sessions", nil, &ss)
}

func (c *Client) Retrigger(ctx context.Context, id int64) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/v1/prs/%d/retrigger", id), nil, nil)
}

func (c *Client) SetPaused(ctx context.Context, id int64, paused bool) error {
	action := "resume"
	if paused {
		action = "pause"
	}
	return c.do(ctx, "POST", fmt.Sprintf("/api/v1/prs/%d/%s", id, action), nil, nil)
}

// TaskQuery filters ListTasks; zero values mean "any".
type TaskQuery struct {
	Status string
	PRID   int64
	Limit  int
}

func (c *Client) ListTasks(ctx context.Context, q TaskQuery) ([]core.Task, error) {
	v := url.Values{}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.PRID != 0 {
		v.Set("pr", strconv.FormatInt(q.PRID, 10))
	}
	if q.Limit != 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	var tasks []core.Task
	return tasks, c.do(ctx, "GET", "/api/v1/tasks?"+v.Encode(), nil, &tasks)
}

// Health returns the daemon's tool health report; refresh re-runs the checks.
func (c *Client) Health(ctx context.Context, refresh bool) (health.Report, error) {
	var r health.Report
	path := "/api/v1/health"
	if refresh {
		path += "?refresh=1"
	}
	return r, c.do(ctx, "GET", path, nil, &r)
}

// Report fetches PR lifecycle stats; days <= 0 means all time.
func (c *Client) Report(ctx context.Context, days int) (core.Report, error) {
	var r core.Report
	path := "/api/v1/report"
	if days > 0 {
		path += "?days=" + strconv.Itoa(days)
	}
	return r, c.do(ctx, "GET", path, nil, &r)
}

// SlackTest asks the daemon to send a test message with its configured bot.
func (c *Client) SlackTest(ctx context.Context) error {
	return c.do(ctx, "POST", "/api/v1/slack/test", nil, nil)
}

// Resolve turns a CLI argument (numeric id, owner/repo#N, host/owner/repo#N
// or a PR URL) into an id. A short ref without a host matches on any host, as
// long as only one watched PR fits.
func (c *Client) Resolve(ctx context.Context, arg string) (int64, error) {
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return id, nil
	}
	const noHost = "-"
	ref, err := core.ParseRef(arg, noHost)
	if err != nil {
		return 0, err
	}
	prs, err := c.ListPRs(ctx)
	if err != nil {
		return 0, err
	}
	var found []core.PR
	for _, p := range prs {
		if p.Owner == ref.Owner && p.Repo == ref.Repo && p.Number == ref.Number && (ref.Host == noHost || p.Host == ref.Host) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("%s is not watched", arg)
	case 1:
		return found[0].ID, nil
	}
	return 0, fmt.Errorf("%s matches PRs on several hosts; use host/owner/repo#N or the id", arg)
}
