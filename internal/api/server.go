// Package api is the daemon's HTTP API and the Go client the CLI uses.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binarycat0/watchdog/internal/agent"
	"github.com/binarycat0/watchdog/internal/config"
	"github.com/binarycat0/watchdog/internal/core"
	"github.com/binarycat0/watchdog/internal/health"
	"github.com/binarycat0/watchdog/internal/slack"
	"github.com/binarycat0/watchdog/internal/worker"
)

// Server holds the daemon's live state that handlers need.
type Server struct {
	Store        *core.Store
	Pool         *worker.Pool
	Web          fs.FS // static UI, embedded into the binary
	Version      string
	DBPath       string
	Slack        slack.Poster  // nil when there is no Slack token
	SlackChannel func() string // current DM user or channel
	Live         *config.Live  // settings editable from the UI; nil disables /settings
	// Agent lists and validates agent sessions; nil stores links unchecked.
	Agent AgentDirectory
	// GitHubHosts lists the hosts gh is signed in to; nil skips the check.
	GitHubHosts func(ctx context.Context) ([]string, error)
	// Health runs the tool checks. Results are cached for healthTTL because
	// they shell out to gh and call Slack.
	Health  func(ctx context.Context) health.Report
	Log     *slog.Logger
	started time.Time

	healthMu   sync.Mutex
	lastHealth *health.Report
}

const healthTTL = time.Minute

// health returns a cached report, re-running the checks when stale or forced.
func (s *Server) health(ctx context.Context, force bool) health.Report {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if force || s.lastHealth == nil || time.Since(s.lastHealth.CheckedAt) > healthTTL {
		rep := s.Health(ctx)
		s.lastHealth = &rep
	}
	return *s.lastHealth
}

// StatusResponse is returned by GET /api/v1/status.
type StatusResponse struct {
	Version   string      `json:"version"`
	PID       int         `json:"pid"`
	StartedAt time.Time   `json:"started_at"`
	Uptime    string      `json:"uptime"`
	DBPath    string      `json:"db_path"`
	PRs       int         `json:"prs"`
	Running   []core.Task `json:"running"`
	Slack     bool        `json:"slack"`
}

// PRDetail is a PR plus its recent history.
type PRDetail struct {
	core.PR
	URL    string       `json:"url"`
	Events []core.Event `json:"events"`
}

type addRequest struct {
	Ref string `json:"ref"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Handler builds the router. Go 1.22+ ServeMux understands "METHOD /path/{param}"
// patterns, so no third-party router is needed.
func (s *Server) Handler(pid int) http.Handler {
	s.started = time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		prs, err := s.Store.ListPRs(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, StatusResponse{
			Version: s.Version, PID: pid, StartedAt: s.started,
			Uptime: time.Since(s.started).Round(time.Second).String(),
			DBPath: s.DBPath, PRs: len(prs), Running: s.Pool.Running(),
			Slack: s.slackTarget() != "",
		})
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if s.Health == nil {
			writeJSON(w, http.StatusNotImplemented, errorResponse{"health checks not configured"})
			return
		}
		writeJSON(w, http.StatusOK, s.health(r.Context(), r.URL.Query().Get("refresh") != ""))
	})
	mux.HandleFunc("GET /api/v1/prs", func(w http.ResponseWriter, r *http.Request) {
		prs, err := s.Store.ListPRs(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, prs)
	})
	mux.HandleFunc("POST /api/v1/prs", func(w http.ResponseWriter, r *http.Request) {
		var req addRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid JSON body"})
			return
		}
		ref, err := core.ParseRef(req.Ref, s.defaultHost())
		if err != nil {
			writeErr(w, err)
			return
		}
		// Only accept hosts gh can talk to; otherwise every check would fail.
		if s.GitHubHosts != nil {
			hosts, err := s.GitHubHosts(r.Context())
			if err != nil {
				writeJSON(w, http.StatusBadGateway, errorResponse{"can't read gh login status: " + err.Error()})
				return
			}
			if !slices.Contains(hosts, ref.Host) {
				writeJSON(w, http.StatusBadRequest, errorResponse{fmt.Sprintf(
					"gh is not signed in to %s (signed in to: %s); run: gh auth login --hostname %s",
					ref.Host, strings.Join(hosts, ", "), ref.Host)})
				return
			}
		}
		pr, err := s.Store.AddPR(r.Context(), ref)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.Pool.Poke() // new PRs are due immediately
		writeJSON(w, http.StatusCreated, pr)
	})
	mux.HandleFunc("GET /api/v1/prs/{id}", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		evs, err := s.Store.Events(r.Context(), pr.ID, 50)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, PRDetail{PR: pr, URL: pr.URL(), Events: evs})
	}))
	mux.HandleFunc("DELETE /api/v1/prs/{id}", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		if err := s.Store.RemovePR(r.Context(), pr.ID); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /api/v1/prs/{id}/check", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		if _, err := s.Store.EnqueueTask(r.Context(), core.NewTask{
			Kind: worker.KindPRCheck, PRID: pr.ID, DedupeKey: worker.PRCheckDedupe(pr.ID),
		}); err != nil {
			writeErr(w, err)
			return
		}
		s.Pool.Poke()
		w.WriteHeader(http.StatusAccepted)
	}))
	mux.HandleFunc("GET /api/v1/agent/sessions", func(w http.ResponseWriter, r *http.Request) {
		if s.Agent == nil {
			writeJSON(w, http.StatusNotImplemented, errorResponse{"no agent configured"})
			return
		}
		home, _ := os.UserHomeDir()
		sessions, err := s.Agent.Sessions(r.Context(), home)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	})
	mux.HandleFunc("PUT /api/v1/prs/{id}/agent", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		var req struct {
			Session  string `json:"session"`
			Worktree string `json:"worktree"` // optional; taken from the session if empty
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid JSON body"})
			return
		}
		// Like the Python pr-watchdog: only link a session that exists, isn't
		// archived and really works in that worktree.
		if req.Session != "" && s.Agent != nil {
			sess, err := s.Agent.Resolve(r.Context(), req.Session, req.Worktree)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
				return
			}
			req.Worktree = sess.Directory
		}
		if req.Session == "" {
			req.Worktree = ""
		}
		if err := s.Store.SetAgentSession(r.Context(), pr.ID, req.Session, req.Worktree); err != nil {
			writeErr(w, err)
			return
		}
		msg := "linked to agent session " + req.Session + " in " + req.Worktree
		if req.Session == "" {
			msg = "agent session unlinked"
		}
		s.Store.AddEvent(r.Context(), pr.ID, "agent", msg)
		w.WriteHeader(http.StatusNoContent)
	}))
	// Send a prompt to the PR's agent session now. It obeys the mode of the
	// matching rule (ci_failed / review_threads): only "on" really sends;
	// "dry-run" or "off" builds the prompt and records it without sending.
	mux.HandleFunc("POST /api/v1/prs/{id}/agent/prompt", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		var req struct {
			Prompt string `json:"prompt"` // ci_failed or review_threads
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid JSON body"})
			return
		}
		if req.Prompt != worker.PromptCIFailed && req.Prompt != worker.PromptReviewThreads {
			writeJSON(w, http.StatusBadRequest, errorResponse{"prompt must be ci_failed or review_threads"})
			return
		}
		if pr.AgentSession == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{"link an agent session first"})
			return
		}
		head := ""
		if pr.Snapshot != nil {
			head = pr.Snapshot.HeadSHA
		}
		dryRun := s.ruleMode(req.Prompt) != worker.ModeOn
		id, err := s.Store.EnqueueTask(r.Context(), core.NewTask{
			Kind: worker.KindAgentPrompt, PRID: pr.ID,
			Payload:   worker.ActionPayload{Rule: "manual", DryRun: dryRun, HeadSHA: head, Prompt: req.Prompt},
			DedupeKey: fmt.Sprintf("%s:%d:%s", worker.KindAgentPrompt, pr.ID, req.Prompt),
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		if id == 0 {
			writeJSON(w, http.StatusConflict, errorResponse{"that prompt is already queued for this PR"})
			return
		}
		msg := "prompt (" + req.Prompt + ") requested by user"
		if dryRun {
			msg = "[dry-run] " + msg + "; " + req.Prompt + " rule is not on, so it won't be sent"
		}
		s.Store.AddEvent(r.Context(), pr.ID, "agent", msg)
		s.Pool.Poke()
		writeJSON(w, http.StatusAccepted, map[string]bool{"dry_run": dryRun})
	}))
	mux.HandleFunc("POST /api/v1/prs/{id}/retrigger", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		if _, err := s.Store.EnqueueTask(r.Context(), core.NewTask{
			Kind: worker.KindPRRetrigger, PRID: pr.ID, DedupeKey: fmt.Sprintf("%s:%d", worker.KindPRRetrigger, pr.ID),
		}); err != nil {
			writeErr(w, err)
			return
		}
		s.Pool.Poke()
		w.WriteHeader(http.StatusAccepted)
	}))
	mux.HandleFunc("GET /api/v1/report", func(w http.ResponseWriter, r *http.Request) {
		var since *time.Time
		if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
			t := time.Now().AddDate(0, 0, -d)
			since = &t
		}
		prs, err := s.Store.ListPRs(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		fails, err := s.Store.CIFailureCounts(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		rep := core.BuildReport(prs, fails, since, time.Now())
		var from time.Time
		if since != nil {
			from = *since
		}
		runs, err := s.Store.CIRunsSince(r.Context(), from)
		if err != nil {
			writeErr(w, err)
			return
		}
		rep.AddCIRuns(runs)
		writeJSON(w, http.StatusOK, rep)
	})
	mux.HandleFunc("GET /api/v1/prs/{id}/ci-runs", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		runs, err := s.Store.CIRunsForPR(r.Context(), pr.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, runs)
	}))
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := core.TaskFilter{Status: q.Get("status")}
		f.PRID, _ = strconv.ParseInt(q.Get("pr"), 10, 64)
		f.Limit, _ = strconv.Atoi(q.Get("limit"))
		tasks, err := s.Store.ListTasks(r.Context(), f)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tasks)
	})
	mux.HandleFunc("POST /api/v1/slack/test", func(w http.ResponseWriter, r *http.Request) {
		if s.slackTarget() == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{"slack is not configured: set slack.bot_token and slack.channel in config.json, then restart the daemon"})
			return
		}
		if err := s.Slack.Post(r.Context(), s.slackTarget(), ":dog: watchdog test message"); err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for path, paused := range map[string]bool{"pause": true, "resume": false} {
		paused := paused // capture per iteration (needed before Go 1.22, harmless now)
		mux.HandleFunc("POST /api/v1/prs/{id}/"+path, s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
			if err := s.Store.SetPaused(r.Context(), pr.ID, paused); err != nil {
				writeErr(w, err)
				return
			}
			kind := "resumed"
			if paused {
				kind = "paused"
			}
			s.Store.AddEvent(r.Context(), pr.ID, kind, kind+" by user")
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	if s.Live != nil {
		s.settingsRoutes(mux)
	}
	if s.Web != nil {
		// Embedded files have no modification time, so browsers would cache
		// them heuristically and keep an old UI after an upgrade.
		static := http.FileServerFS(s.Web)
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			static.ServeHTTP(w, r)
		})
	}
	return localOnly(mux)
}

// AgentDirectory is what the API needs from the agent (agent.Opencode).
type AgentDirectory interface {
	Sessions(ctx context.Context, dir string) ([]agent.Session, error)
	Resolve(ctx context.Context, id, worktree string) (agent.Session, error)
}

// ruleMode is a workflow rule's current mode; without live settings
// everything counts as dry-run.
func (s *Server) ruleMode(rule string) string {
	if s.Live == nil {
		return worker.ModeDryRun
	}
	if m := s.Live.Get().Workflows[rule]; m != "" {
		return m
	}
	return worker.ModeOff
}

// defaultHost is the host for short refs like owner/repo#123.
func (s *Server) defaultHost() string {
	if s.Live == nil {
		return core.DefaultHost
	}
	return s.Live.Get().GitHubHost
}

// slackTarget is "" unless there is both a token and a recipient.
func (s *Server) slackTarget() string {
	if s.Slack == nil || s.SlackChannel == nil {
		return ""
	}
	return s.SlackChannel()
}

// SettingsResponse is the settings page's data: current values plus the
// choices the form offers.
type SettingsResponse struct {
	Settings     config.Settings `json:"settings"`
	Rules        []RuleInfo      `json:"rules"`
	Modes        []string        `json:"modes"`
	MergeMethods []string        `json:"merge_methods"`
	ChangeKinds  []string        `json:"change_kinds"`
	ConfigPath   string          `json:"config_path"`
}

// RuleInfo describes a workflow rule, and for a PR, its current state.
type RuleInfo struct {
	Name    string        `json:"name"`
	Action  string        `json:"action"`
	Mode    string        `json:"mode,omitempty"`
	Matches bool          `json:"matches"`
	LastRun *core.RuleRun `json:"last_run,omitempty"`
}

func ruleNames() []string {
	names := make([]string, len(worker.Rules))
	for i, r := range worker.Rules {
		names[i] = r.Name
	}
	return names
}

func (s *Server) settingsResponse() SettingsResponse {
	resp := SettingsResponse{
		Settings: s.Live.Settings(), Modes: config.Modes, MergeMethods: config.MergeMethods, ConfigPath: s.Live.ConfigPath(),
		ChangeKinds: []string{core.ChangeCIFailed, core.ChangeCIPassed, core.ChangeConflict, core.ChangeConflictResolved,
			core.ChangeChangesRequested, core.ChangeApproved, core.ChangeNewComments, core.ChangePushed,
			core.ChangeMerged, core.ChangeClosed},
	}
	for _, r := range worker.Rules {
		resp.Rules = append(resp.Rules, RuleInfo{Name: r.Name, Action: r.Action})
	}
	return resp
}

func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.settingsResponse())
	})
	mux.HandleFunc("PUT /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		var in config.Settings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid JSON body: " + err.Error()})
			return
		}
		if err := in.Validate(ruleNames()); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
			return
		}
		if err := s.Live.Update(in); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.settingsResponse())
	})
	mux.HandleFunc("GET /api/v1/prs/{id}/rules", s.withPR(func(w http.ResponseWriter, r *http.Request, pr core.PR) {
		last, err := s.Store.LastRuleRuns(r.Context(), pr.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		modes := s.Live.Get().Workflows
		var out []RuleInfo
		for _, rule := range worker.Rules {
			info := RuleInfo{Name: rule.Name, Action: rule.Action, Mode: modes[rule.Name]}
			if info.Mode == "" {
				info.Mode = "off"
			}
			if pr.Snapshot != nil {
				_, info.Matches = rule.Match(*pr.Snapshot)
			}
			if run, ok := last[rule.Name]; ok {
				info.LastRun = &run
			}
			out = append(out, info)
		}
		writeJSON(w, http.StatusOK, out)
	}))
}

// withPR resolves the {id} path segment and 404s if the PR is unknown.
func (s *Server) withPR(h func(http.ResponseWriter, *http.Request, core.PR)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"id must be a number"})
			return
		}
		pr, err := s.Store.GetPR(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		h(w, r, pr)
	}
}

// localOnly rejects requests whose Host is not loopback (DNS rebinding) and
// state-changing requests from another origin (a web page you visit POSTing to
// localhost). The API has no auth, so these two checks are what keep it local.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeJSON(w, http.StatusForbidden, errorResponse{"non-local Host"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					writeJSON(w, http.StatusForbidden, errorResponse{"cross-origin request"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeErr maps domain errors to HTTP codes. errors.Is checks wrapped errors
// too, similar to isinstance on an exception chain.
func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, core.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, core.ErrExists):
		code = http.StatusConflict
	case errors.Is(err, core.ErrBadRef):
		code = http.StatusBadRequest
	}
	writeJSON(w, code, errorResponse{err.Error()})
}
