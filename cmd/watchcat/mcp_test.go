package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binarycat0/watchcat/internal/agent"
	"github.com/binarycat0/watchcat/internal/api"
	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/worker"
)

// fakeOpencode knows a set of sessions and validates like agent.Opencode.
type fakeOpencode struct{ sessions []agent.Session }

func (f fakeOpencode) Sessions(context.Context, string) ([]agent.Session, error) {
	return f.sessions, nil
}
func (f fakeOpencode) Resolve(_ context.Context, id, worktree string) (agent.Session, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return agent.Session{}, errors.New("session " + id + " not found")
}

func newMCPTest(t *testing.T, sessions []agent.Session, cwd string) (*mcpTools, *core.Store) {
	t.Helper()
	store, err := core.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := &api.Server{Store: store, Pool: &worker.Pool{Store: store}, Agent: fakeOpencode{sessions}}
	ts := httptest.NewServer(srv.Handler(1))
	t.Cleanup(ts.Close)
	return &mcpTools{cl: &api.Client{Base: ts.URL, HTTP: ts.Client()}, cwd: cwd}, store
}

func args(m map[string]string) json.RawMessage { b, _ := json.Marshal(m); return b }

func TestMCPWatchDetectsSession(t *testing.T) {
	wt := t.TempDir()
	tools, store := newMCPTest(t, []agent.Session{
		{ID: "ses_mine", Title: "fix flaky test", Directory: wt},
		{ID: "ses_other", Title: "other", Directory: "/elsewhere"},
		{ID: "ses_parent", Title: "in a parent dir", Directory: filepath.Dir(wt)},
	}, filepath.Join(wt)) // the agent runs in its worktree
	ctx := context.Background()

	out, err := tools.watchPR(ctx, args(map[string]string{"pr": "https://github.com/o/r/pull/7"}))
	if err != nil || !strings.Contains(out, "linked to session ses_mine") {
		t.Fatalf("%q %v", out, err)
	}
	pr, _ := store.FindPR(ctx, core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 7})
	if pr.AgentSession != "ses_mine" || pr.AgentWorktree != wt {
		t.Fatalf("binding: %+v", pr)
	}
	// Calling again is fine (already watched) and keeps the link.
	if _, err := tools.watchPR(ctx, args(map[string]string{"pr": "o/r#7"})); err != nil {
		t.Fatal(err)
	}

	if out, _ := tools.myPRs(ctx, args(nil)); !strings.Contains(out, "o/r#7") {
		t.Fatalf("my_prs: %q", out)
	}
	snap := core.Snapshot{Title: "Fix", State: "OPEN", HeadSHA: "abcdef12", CI: core.CIFailure,
		FailedChecks: []core.Check{{Name: "unit", URL: "https://ci/1"}}, Unresolved: 2}
	store.RecordCheck(ctx, pr.ID, core.CheckResult{Status: core.StatusCIFailed, Summary: "CI failed: unit", Snapshot: &snap}, time.Now(), time.Now())
	if out, err := tools.prStatus(ctx, args(map[string]string{"pr": "o/r#7"})); err != nil ||
		!strings.Contains(out, "failed check: unit https://ci/1") || !strings.Contains(out, "unresolved threads: 2") {
		t.Fatalf("pr_status: %q %v", out, err)
	}
	if out, err := tools.unwatchPR(ctx, args(map[string]string{"pr": "o/r#7"})); err != nil || !strings.Contains(out, "paused") {
		t.Fatalf("unwatch: %q %v", out, err)
	}
	if pr, _ = store.GetPR(ctx, pr.ID); pr.AgentSession != "" || !pr.Paused {
		t.Fatalf("after unwatch: %+v", pr)
	}
}

func TestMCPSessionAmbiguity(t *testing.T) {
	wt := t.TempDir()
	tools, _ := newMCPTest(t, []agent.Session{
		{ID: "ses_a", Title: "a", Directory: wt},
		{ID: "ses_b", Title: "b", Directory: wt},
	}, wt)
	ctx := context.Background()
	_, err := tools.watchPR(ctx, args(map[string]string{"pr": "o/r#1"}))
	if err == nil || !strings.Contains(err.Error(), "several opencode sessions") {
		t.Fatalf("two sessions in one worktree must ask for session_id: %v", err)
	}
	if _, err := tools.watchPR(ctx, args(map[string]string{"pr": "o/r#1", "session_id": "ses_b"})); err != nil {
		t.Fatal(err)
	}

	lonely, _ := newMCPTest(t, nil, t.TempDir())
	if _, err := lonely.watchPR(ctx, args(map[string]string{"pr": "o/r#1"})); err == nil || !strings.Contains(err.Error(), "no open opencode session") {
		t.Fatalf("no session: %v", err)
	}
	if _, err := lonely.prStatus(ctx, args(map[string]string{"pr": "o/r#99"})); err == nil || !strings.Contains(err.Error(), "call watch_pr first") {
		t.Fatalf("unwatched PR: %v", err)
	}
}

func TestMCPDaemonDown(t *testing.T) {
	tools := &mcpTools{cl: api.NewClient("127.0.0.1:1"), cwd: t.TempDir()}
	if _, err := tools.health(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "watchcat service start") {
		t.Fatalf("%v", err)
	}
}
