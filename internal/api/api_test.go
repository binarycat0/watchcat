package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/binarycat0/watchcat/internal/agent"
	"github.com/binarycat0/watchcat/internal/config"
	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/templates"
	"github.com/binarycat0/watchcat/internal/worker"
)

type fakeGitHub struct{}

func (fakeGitHub) Fetch(ctx context.Context, ref core.PRRef) (core.Snapshot, error) {
	return core.Snapshot{Title: "t", State: "OPEN", Mergeable: "MERGEABLE", CI: core.CISuccess}, nil
}

// TestEndToEnd runs the real handler + scheduler against a temp database and
// drives it through the same Client the CLI uses.
func TestEndToEnd(t *testing.T) {
	store, err := core.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pool := &worker.Pool{
		Store: store,
		Handlers: map[string]worker.Handler{
			worker.KindPRCheck: &worker.PRCheck{Store: store, GitHub: fakeGitHub{}, Interval: worker.Fixed(time.Hour)},
		},
		Planners: []worker.Planner{worker.PRPlanner(store, worker.Fixed(time.Hour))},
		Tick:     20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Run(ctx)

	srv := &Server{Store: store, Pool: pool, Version: "test"}
	ts := httptest.NewServer(srv.Handler(1))
	defer ts.Close()
	cl := &Client{Base: ts.URL, HTTP: ts.Client()}

	pr, err := cl.AddPR(ctx, "https://github.com/o/r/pull/5")
	if err != nil {
		t.Fatal(err)
	}
	var apiErr *APIError
	if _, err := cl.AddPR(ctx, "https://github.com/o/r/pull/5"); !errors.As(err, &apiErr) || apiErr.Code != http.StatusConflict {
		t.Fatalf("duplicate add: %v", err)
	}
	if _, err := cl.AddPR(ctx, "nonsense"); !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest {
		t.Fatalf("bad ref: %v", err)
	}

	// The scheduler should pick the new PR up and record the fake result.
	deadline := time.Now().Add(2 * time.Second)
	for {
		d, err := cl.GetPR(ctx, pr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status == core.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PR never checked: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}

	id, err := cl.Resolve(ctx, "https://github.com/o/r/pull/5")
	if err != nil || id != pr.ID {
		t.Fatalf("resolve: %d %v", id, err)
	}
	if err := cl.SetPaused(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := cl.CheckNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	tasks, err := cl.ListTasks(ctx, TaskQuery{PRID: id})
	if err != nil || len(tasks) < 2 || tasks[len(tasks)-1].Status != core.TaskDone {
		t.Fatalf("tasks: %+v %v", tasks, err)
	}
	if err := cl.SlackTest(ctx); !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest {
		t.Fatalf("slack test without config: %v", err)
	}
	st, err := cl.Status(ctx)
	if err != nil || st.PRs != 1 {
		t.Fatalf("status: %+v %v", st, err)
	}
	if err := cl.RemovePR(ctx, id); err != nil {
		t.Fatal(err)
	}
	if prs, _ := cl.ListPRs(ctx); len(prs) != 0 {
		t.Fatalf("expected empty list, got %d", len(prs))
	}
}

func TestLocalOnly(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cases := []struct {
		method, host, origin string
		want                 int
	}{
		{"GET", "127.0.0.1:7878", "", 200},
		{"GET", "localhost:7878", "", 200},
		{"GET", "evil.example:7878", "", 403}, // DNS rebinding
		{"POST", "127.0.0.1:7878", "http://127.0.0.1:7878", 200},
		{"POST", "127.0.0.1:7878", "https://evil.example", 403}, // cross-site POST
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "/api/v1/prs", strings.NewReader("{}"))
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s host=%s origin=%s: got %d want %d", c.method, c.host, c.origin, w.Code, c.want)
		}
	}
}

func TestClientDaemonDown(t *testing.T) {
	cl := NewClient("127.0.0.1:1") // nothing listens on port 1
	if _, err := cl.Status(context.Background()); !errors.Is(err, ErrDaemonDown) {
		t.Fatalf("got %v, want ErrDaemonDown", err)
	}
}

func TestSettingsAndRules(t *testing.T) {
	dir := t.TempDir()
	paths := config.PathsIn(dir)
	config.Init(paths)
	cfg, _ := config.Load(paths)
	cfg.Slack.BotToken = "xoxb-in-memory-only-123"
	live := config.NewLive(cfg, paths)

	store, _ := core.Open(paths.DB)
	defer store.Close()
	srv := &Server{Store: store, Pool: &worker.Pool{Store: store}, Live: live}
	ts := httptest.NewServer(srv.Handler(1))
	defer ts.Close()

	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := do("GET", "/api/v1/settings", "")
	if code != 200 || !strings.Contains(body, `"merge":"dry-run"`) || strings.Contains(body, "xoxb") {
		t.Fatalf("GET settings: %d %s", code, body)
	}
	if code, _ := do("PUT", "/api/v1/settings", `{"workflows":{"merge":"maybe"},"merge_method":"squash","poll_interval":"5m"}`); code != 400 {
		t.Fatalf("invalid mode should be rejected, got %d", code)
	}
	code, body = do("PUT", "/api/v1/settings",
		`{"workflows":{"merge":"on"},"merge_method":"rebase","poll_interval":"10m","notify_on":["ci_failed"],"slack_user_id":"U1"}`)
	if code != 200 || live.Get().Workflows["merge"] != "on" || live.Get().PollInterval.Duration != 10*time.Minute {
		t.Fatalf("PUT settings: %d %s", code, body)
	}
	if b, _ := os.ReadFile(paths.Config); strings.Contains(string(b), "xoxb") {
		t.Fatal("token must never be written to config.json")
	}

	pr, _ := store.AddPR(context.Background(), core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	snap := core.Snapshot{State: "OPEN", CI: core.CIFailure, HeadSHA: "a"}
	store.RecordCheck(context.Background(), pr.ID, core.CheckResult{Status: core.StatusCIFailed, Snapshot: &snap}, time.Now(), time.Now())
	store.MarkRuleFired(context.Background(), pr.ID, "ci_failed", "a:", "dry-run")
	code, body = do("GET", "/api/v1/prs/"+strconv.FormatInt(pr.ID, 10)+"/rules", "")
	var rules []RuleInfo
	json.Unmarshal([]byte(body), &rules)
	if code != 200 || len(rules) != 4 {
		t.Fatalf("rules: %d %s", code, body)
	}
	for _, r := range rules {
		if r.Name == "ci_failed" && (!r.Matches || r.LastRun == nil || r.Mode != "dry-run") {
			t.Fatalf("ci_failed: %+v", r)
		}
		if r.Name == "merge" && (r.Matches || r.Mode != "on") {
			t.Fatalf("merge: %+v", r)
		}
	}
}

func TestAddPRChecksSignedInHosts(t *testing.T) {
	store, _ := core.Open(filepath.Join(t.TempDir(), "db"))
	defer store.Close()
	srv := &Server{Store: store, Pool: &worker.Pool{Store: store},
		GitHubHosts: func(context.Context) ([]string, error) { return []string{"github.com", "ghe.corp"}, nil }}
	ts := httptest.NewServer(srv.Handler(1))
	defer ts.Close()
	cl := &Client{Base: ts.URL, HTTP: ts.Client()}
	ctx := context.Background()

	pr, err := cl.AddPR(ctx, "https://ghe.corp/team/app/pull/9")
	if err != nil || pr.Host != "ghe.corp" {
		t.Fatalf("GHE PR on a signed-in host: %+v %v", pr, err)
	}
	var apiErr *APIError
	_, err = cl.AddPR(ctx, "https://other.corp/team/app/pull/9")
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest || !strings.Contains(err.Error(), "gh auth login --hostname other.corp") {
		t.Fatalf("unknown host should be rejected with a hint: %v", err)
	}
	if id, err := cl.Resolve(ctx, "https://ghe.corp/team/app/pull/9"); err != nil || id != pr.ID {
		t.Fatalf("URL should find the GHE PR: %d %v", id, err)
	}
	if _, err := cl.Resolve(ctx, "https://github.com/team/app/pull/9"); err == nil {
		t.Fatal("same owner/repo#N on another host must not match")
	}
	if _, err := cl.Resolve(ctx, "team/app#9"); !errors.Is(err, core.ErrBadRef) {
		t.Fatalf("short refs are not accepted: %v", err)
	}
	if _, err = cl.AddPR(ctx, "team/app#9"); err == nil || !strings.Contains(err.Error(), "PR URL") {
		t.Fatalf("adding a short ref should ask for a URL: %v", err)
	}
}

type fakeAgents struct{}

func (fakeAgents) Sessions(ctx context.Context, dir string) ([]agent.Session, error) {
	return []agent.Session{{ID: "ses_a", Title: "PR 9", Directory: "/w/app"}}, nil
}
func (fakeAgents) Resolve(ctx context.Context, id, worktree string) (agent.Session, error) {
	if id != "ses_a" {
		return agent.Session{}, errors.New("session " + id + " not found")
	}
	if worktree != "" && worktree != "/w/app" {
		return agent.Session{}, errors.New("session ses_a works in /w/app, not " + worktree)
	}
	return agent.Session{ID: "ses_a", Directory: "/w/app"}, nil
}

func TestBindAgentValidates(t *testing.T) {
	store, _ := core.Open(filepath.Join(t.TempDir(), "db"))
	defer store.Close()
	srv := &Server{Store: store, Pool: &worker.Pool{Store: store}, Agent: fakeAgents{}}
	ts := httptest.NewServer(srv.Handler(1))
	defer ts.Close()
	cl := &Client{Base: ts.URL, HTTP: ts.Client()}
	ctx := context.Background()
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})

	if ss, err := cl.AgentSessions(ctx); err != nil || len(ss) != 1 {
		t.Fatalf("sessions: %v %v", ss, err)
	}
	if err := cl.BindAgent(ctx, pr.ID, "ses_a", ""); err != nil {
		t.Fatal(err)
	}
	if d, _ := cl.GetPR(ctx, pr.ID); d.AgentSession != "ses_a" || d.AgentWorktree != "/w/app" {
		t.Fatalf("worktree should come from the session: %+v", d.PR)
	}
	if err := cl.BindAgent(ctx, pr.ID, "ses_a", "/other"); err == nil || !strings.Contains(err.Error(), "works in /w/app") {
		t.Fatalf("mismatched worktree must be rejected: %v", err)
	}
	if err := cl.BindAgent(ctx, pr.ID, "ses_zzz", ""); err == nil {
		t.Fatal("unknown session must be rejected")
	}
	if err := cl.BindAgent(ctx, pr.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if d, _ := cl.GetPR(ctx, pr.ID); d.AgentSession != "" || d.AgentWorktree != "" {
		t.Fatalf("unbind should clear both: %+v", d.PR)
	}
}

func TestManualPromptObeysRuleMode(t *testing.T) {
	dir := t.TempDir()
	paths := config.PathsIn(dir)
	config.Init(paths)
	cfg, _ := config.Load(paths) // every rule defaults to dry-run
	live := config.NewLive(cfg, paths)
	store, _ := core.Open(paths.DB)
	defer store.Close()
	srv := &Server{Store: store, Pool: &worker.Pool{Store: store}, Live: live}
	ts := httptest.NewServer(srv.Handler(1))
	defer ts.Close()
	cl := &Client{Base: ts.URL, HTTP: ts.Client()}
	ctx := context.Background()
	pr, _ := store.AddPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	store.SetAgentSession(ctx, pr.ID, "ses_a", "/w")

	payload := func() worker.ActionPayload {
		ts, _ := store.ListTasks(ctx, core.TaskFilter{Kind: worker.KindAgentPrompt})
		var p worker.ActionPayload
		json.Unmarshal(ts[0].Payload, &p)
		return p
	}

	dry, err := cl.PromptAgent(ctx, pr.ID, "ci_failed")
	if err != nil || !dry || !payload().DryRun {
		t.Fatalf("dry-run rule: manual prompt must not send: dry=%v err=%v payload=%+v", dry, err, payload())
	}

	s := live.Settings()
	s.Workflows["ci_failed"] = "on"
	live.Update(s)
	store.CompleteTask(ctx, 1) // free the dedupe key
	dry, err = cl.PromptAgent(ctx, pr.ID, "ci_failed")
	if err != nil || dry || payload().DryRun {
		t.Fatalf("rule on: manual prompt should send: dry=%v err=%v payload=%+v", dry, err, payload())
	}
}

func TestTemplates(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{Templates: &templates.Set{Dir: dir}}
	ts := httptest.NewServer(srv.Handler(1))
	defer ts.Close()
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := do("GET", "/api/v1/templates", "")
	var list []templates.Info
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list) != len(templates.Defs) {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, body = do("POST", "/api/v1/templates/notify_change/preview", `{"text":"hi {{.PR.Ref}}"}`); code != 200 || !strings.Contains(body, "hi acme/api#42") {
		t.Fatalf("preview: %d %s", code, body)
	}
	if code, body = do("PUT", "/api/v1/templates/notify_change", `{"text":"{{.Bad}}"}`); code != 400 {
		t.Fatalf("bad save: %d %s", code, body)
	}
	if code, body = do("PUT", "/api/v1/templates/notify_change", `{"text":"hi {{.PR.Ref}}"}`); code != 200 || !strings.Contains(body, `"custom":true`) {
		t.Fatalf("save: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "notify_change.tmpl")); err != nil {
		t.Fatal(err)
	}
	if code, body = do("DELETE", "/api/v1/templates/notify_change", ""); code != 200 || !strings.Contains(body, `"custom":false`) {
		t.Fatalf("restore: %d %s", code, body)
	}
	if code, _ = do("GET", "/api/v1/templates/nope", ""); code != 404 {
		t.Fatalf("unknown: %d", code)
	}
}
