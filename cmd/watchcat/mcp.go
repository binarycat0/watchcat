package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/binarycat0/watchcat/internal/agent"
	"github.com/binarycat0/watchcat/internal/api"
	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/mcp"
)

const mcpInstructions = `watchcat watches pull requests for the user: CI results, reviews,
merge conflicts. Link a PR to your session with watch_pr right after you
create it (e.g. after "gh pr create"); watchcat then checks it on a schedule
and may queue prompts back into your session when CI fails or reviewers
leave unresolved threads, depending on the user's settings.
Use pr_status to see a PR's checks, failing jobs and review state.
These tools never merge, retrigger CI or change settings; ask the user for that.
Arguments are optional: the PR defaults to the current branch's PR and the
session to the opencode session working in this directory.`

// mcpTools are the MCP tools, backed by the daemon API. cwd is the agent's
// working directory: opencode starts this process in the session's worktree.
type mcpTools struct {
	cl  *api.Client
	cwd string
}

var (
	prArg      = map[string]any{"type": "string", "description": "PR URL, e.g. https://<host>/owner/repo/pull/123. Default: the PR of the current git branch."}
	sessionArg = map[string]any{"type": "string", "description": "opencode session ID (ses_...). Default: the session working in this directory."}
)

func schema(props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
}

func cmdMCP(e env, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// The agent runs these one at a time; a slower timeout than the CLI's
	// covers gh/opencode calls the daemon makes on the tool's behalf.
	cl := api.NewClient(e.cfg.Addr())
	cl.HTTP = &http.Client{Timeout: 60 * time.Second}
	t := &mcpTools{cl: cl, cwd: cwd}
	s := &mcp.Server{
		Name: "watchcat", Version: version, Instructions: mcpInstructions,
		Tools: []mcp.Tool{
			{Name: "watch_pr", Description: "Start watching a PR and link it to your opencode session, so watchcat tracks its CI and reviews and can send follow-ups to this session. Call this right after creating a PR.",
				InputSchema: schema(map[string]any{"pr": prArg, "session_id": sessionArg}), Handler: t.watchPR},
			{Name: "pr_status", Description: "Current state of a watched PR: CI result, failing checks with links, review decision, unresolved threads, merge conflicts, and the latest CI job runs with durations.",
				InputSchema: schema(map[string]any{"pr": prArg}), Handler: t.prStatus},
			{Name: "my_prs", Description: "PRs linked to your opencode session, with their status.",
				InputSchema: schema(map[string]any{"session_id": sessionArg}), Handler: t.myPRs},
			{Name: "check_now", Description: "Ask watchcat to re-check a PR now instead of waiting for the next scheduled check (e.g. after you pushed). Read the result with pr_status a minute later.",
				InputSchema: schema(map[string]any{"pr": prArg}), Handler: t.checkNow},
			{Name: "unwatch_pr", Description: "Unlink a PR from your session and pause checks (history is kept). Use when you hand the PR over or abandon it.",
				InputSchema: schema(map[string]any{"pr": prArg}), Handler: t.unwatchPR},
			{Name: "health", Description: "Whether watchcat and the tools it needs (gh, Slack, opencode) are working. Use when another tool fails unexpectedly.",
				InputSchema: schema(map[string]any{}), Handler: t.health},
		},
	}
	// stdout carries the protocol; anything else must go to stderr.
	return s.Serve(context.Background(), os.Stdin, os.Stdout)
}

type prArgs struct {
	PR        string `json:"pr"`
	SessionID string `json:"session_id"`
}

func parseArgs(raw json.RawMessage) (prArgs, error) {
	var a prArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("invalid arguments: %w", err)
	}
	a.PR, a.SessionID = strings.TrimSpace(a.PR), strings.TrimSpace(a.SessionID)
	return a, nil
}

// daemonErr turns "connection refused" into something an agent can relay.
func daemonErr(err error) error {
	if errors.Is(err, api.ErrDaemonDown) {
		return errors.New("the watchcat daemon is not running; ask the user to start it (watchcat service start)")
	}
	return err
}

// prRef returns the PR to act on: the argument, or the PR of the branch
// checked out in the agent's directory (gh works out the host itself).
func (t *mcpTools) prRef(ctx context.Context, arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", "--json", "url", "--jq", ".url")
	cmd.Dir = t.cwd
	out, err := cmd.Output()
	if url := strings.TrimSpace(string(out)); err == nil && url != "" {
		return url, nil
	}
	return "", fmt.Errorf("no PR found for the current branch in %s; pass pr as a PR URL", t.cwd)
}

// watchedID resolves a PR argument (or the current branch's PR) to a watched PR's id.
func (t *mcpTools) watchedID(ctx context.Context, arg string) (int64, string, error) {
	ref, err := t.prRef(ctx, arg)
	if err != nil {
		return 0, "", err
	}
	id, err := t.cl.Resolve(ctx, ref)
	if err != nil {
		if errors.Is(err, api.ErrDaemonDown) {
			return 0, "", daemonErr(err)
		}
		return 0, "", fmt.Errorf("%s is not watched yet; call watch_pr first", ref)
	}
	return id, ref, nil
}

// sessionEnv are variables an agent runtime may set with its session ID.
var sessionEnv = []string{"OPENCODE_SESSION_ID", "OPENCODE_SESSION"}

// session finds the opencode session this MCP server belongs to: the
// argument, an environment variable, or the open session whose directory is
// this one (or the closest one containing it).
func (t *mcpTools) session(ctx context.Context, arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	for _, k := range sessionEnv {
		if v := os.Getenv(k); agent.ValidID(v) {
			return v, nil
		}
	}
	sessions, err := t.cl.AgentSessions(ctx)
	if err != nil {
		return "", fmt.Errorf("can't list opencode sessions (%v); pass session_id", daemonErr(err))
	}
	// The deepest directory wins: a session in the worktree itself beats
	// one in a parent folder like /tmp or the home directory.
	here := realPath(t.cwd)
	var matches []agent.Session
	best := -1
	for _, s := range sessions {
		dir := realPath(s.Directory)
		if here != dir && !strings.HasPrefix(here, dir+string(filepath.Separator)) {
			continue
		}
		switch {
		case len(dir) > best:
			best, matches = len(dir), []agent.Session{s}
		case len(dir) == best:
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", fmt.Errorf("no open opencode session works in %s; pass session_id", t.cwd)
	}
	var list []string
	for _, s := range matches {
		list = append(list, fmt.Sprintf("%s (%s)", s.ID, s.Title))
	}
	return "", fmt.Errorf("several opencode sessions work in %s: %s; pass session_id with your own", t.cwd, strings.Join(list, ", "))
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

func (t *mcpTools) watchPR(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseArgs(raw)
	if err != nil {
		return "", err
	}
	ref, err := t.prRef(ctx, a.PR)
	if err != nil {
		return "", err
	}
	session, err := t.session(ctx, a.SessionID)
	if err != nil {
		return "", err
	}
	var id int64
	pr, err := t.cl.AddPR(ctx, ref)
	var apiErr *api.APIError
	switch {
	case err == nil:
		id = pr.ID
	case errors.As(err, &apiErr) && apiErr.Code == http.StatusConflict:
		if id, err = t.cl.Resolve(ctx, ref); err != nil {
			return "", err
		}
	default:
		return "", daemonErr(err)
	}
	if err := t.cl.BindAgent(ctx, id, session, ""); err != nil {
		return "", fmt.Errorf("watching %s, but linking session %s failed: %w", ref, session, err)
	}
	d, err := t.cl.GetPR(ctx, id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Watching %s, linked to session %s (worktree %s). Watchcat checks it on a schedule; "+
		"depending on the user's settings it may queue prompts into this session when CI fails or review threads need attention. "+
		"Use pr_status to see its state.", d.Ref(), session, d.AgentWorktree), nil
}

func (t *mcpTools) prStatus(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseArgs(raw)
	if err != nil {
		return "", err
	}
	id, _, err := t.watchedID(ctx, a.PR)
	if err != nil {
		return "", err
	}
	d, err := t.cl.GetPR(ctx, id)
	if err != nil {
		return "", daemonErr(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", d.Ref(), d.URL)
	status := d.Status
	if d.Paused {
		status += " (paused)"
	}
	fmt.Fprintf(&b, "status: %s; %s\nlast checked: %s\n", status, d.Summary, ago(d.LastChecked))
	if s := d.Snapshot; s != nil {
		fmt.Fprintf(&b, "title: %s\nhead: %s, base: %s, draft: %v\nCI: %s; mergeable: %s; review: %s; unresolved threads: %d\n",
			s.Title, short(s.HeadSHA), s.BaseRef, s.Draft, s.CI, strings.ToLower(s.Mergeable), orNone(s.ReviewDecision), s.Unresolved)
		for _, c := range s.FailedChecks {
			fmt.Fprintf(&b, "failed check: %s %s\n", c.Name, c.URL)
		}
		if len(s.PendingChecks) > 0 {
			fmt.Fprintf(&b, "pending checks: %s\n", strings.Join(s.PendingChecks, ", "))
		}
		if runs, err := t.cl.CIRuns(ctx, id); err == nil {
			for _, r := range latestRuns(runs, s.HeadSHA) {
				res := r.Result
				if res == "" {
					res = "running"
				}
				fmt.Fprintf(&b, "CI run: %s %s %s\n", r.Name, res, human(r.Duration(time.Now()).Seconds()))
			}
		}
	}
	if d.AgentSession != "" {
		fmt.Fprintf(&b, "linked session: %s\n", d.AgentSession)
	}
	return b.String(), nil
}

// latestRuns keeps the newest run of each job on the head commit.
func latestRuns(runs []core.CIRun, sha string) []core.CIRun {
	seen := map[string]bool{}
	var out []core.CIRun
	for _, r := range runs { // newest first
		if r.HeadSHA == sha && !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r)
		}
	}
	return out
}

func (t *mcpTools) myPRs(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseArgs(raw)
	if err != nil {
		return "", err
	}
	session, err := t.session(ctx, a.SessionID)
	if err != nil {
		return "", err
	}
	prs, err := t.cl.ListPRs(ctx)
	if err != nil {
		return "", daemonErr(err)
	}
	var b strings.Builder
	for _, p := range prs {
		if p.AgentSession == session {
			fmt.Fprintf(&b, "%s %s: %s; %s\n", p.Ref(), p.URL(), p.Status, p.Summary)
		}
	}
	if b.Len() == 0 {
		return "No PRs are linked to session " + session + ". Use watch_pr after creating one.", nil
	}
	return b.String(), nil
}

func (t *mcpTools) checkNow(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseArgs(raw)
	if err != nil {
		return "", err
	}
	id, ref, err := t.watchedID(ctx, a.PR)
	if err != nil {
		return "", err
	}
	if err := t.cl.CheckNow(ctx, id); err != nil {
		return "", daemonErr(err)
	}
	return "Check queued for " + ref + "; call pr_status in about a minute.", nil
}

func (t *mcpTools) unwatchPR(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseArgs(raw)
	if err != nil {
		return "", err
	}
	id, ref, err := t.watchedID(ctx, a.PR)
	if err != nil {
		return "", err
	}
	if err := t.cl.BindAgent(ctx, id, "", ""); err != nil {
		return "", daemonErr(err)
	}
	if err := t.cl.SetPaused(ctx, id, true); err != nil {
		return "", daemonErr(err)
	}
	return "Unlinked " + ref + " from your session and paused its checks. The user can resume it in watchcat.", nil
}

func (t *mcpTools) health(ctx context.Context, _ json.RawMessage) (string, error) {
	rep, err := t.cl.Health(ctx, false)
	if err != nil {
		return "", daemonErr(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "watchcat daemon: running; tools overall: %s\n", rep.Status)
	for _, r := range rep.Results {
		req := "optional"
		if r.Required {
			req = "required"
		}
		fmt.Fprintf(&b, "%s (%s): %s - %s\n", r.Title, req, r.Status, r.Summary)
	}
	return b.String(), nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return strings.ToLower(s)
}
