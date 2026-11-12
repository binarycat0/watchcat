package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fake records calls and answers like opencode's CLI.
type fake struct {
	calls    [][]string
	sessions map[string]string // id -> response JSON for GET /api/session/<id>
	pages    []string          // responses for the session list, in order
	prompt   string
}

func (f *fake) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{dir}, args...))
	method, endpoint := args[1], args[2]
	switch {
	case method == "get" && strings.HasPrefix(endpoint, "/api/session?"):
		p := f.pages[0]
		f.pages = f.pages[1:]
		return []byte(p), nil
	case method == "get":
		if s, ok := f.sessions[strings.TrimPrefix(endpoint, "/api/session/")]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("exit status 1")
	case method == "post":
		return []byte(f.prompt), nil
	}
	return nil, errors.New("unexpected call")
}

func TestSessionsPaginates(t *testing.T) {
	f := &fake{pages: []string{
		`{"data":[{"id":"ses_a","title":"PR 1","location":{"directory":"/w/a"},"time":{}}],"cursor":{"next":"c1"}}`,
		`{"data":[{"id":"ses_b","title":"PR 2","location":{"directory":"/w/b"},"time":{"archived":0}}],"cursor":{"next":"c2"}}`,
		`{"data":[],"cursor":{"next":null}}`,
	}}
	ss, err := Opencode{Run: f.run}.Sessions(context.Background(), "/")
	if err != nil || len(ss) != 2 || ss[1].Directory != "/w/b" || ss[1].Archived {
		t.Fatalf("%+v %v", ss, err)
	}
	if !strings.Contains(f.calls[1][3], "cursor=c1") {
		t.Fatalf("second page should pass the cursor: %v", f.calls[1])
	}
}

func TestResolveAndPrompt(t *testing.T) {
	dir := t.TempDir()
	f := &fake{
		sessions: map[string]string{
			"ses_ok":   `{"data":{"id":"ses_ok","location":{"directory":"` + dir + `"},"time":{}}}`,
			"ses_old":  `{"id":"ses_old","location":{"directory":"` + dir + `"},"time":{"archived":1700000000}}`,
			"ses_away": `{"id":"ses_away","location":{"directory":"/elsewhere"},"time":{}}`,
		},
		prompt: `{"data":{"id":"inbox_1"}}`,
	}
	oc := Opencode{Run: f.run}
	ctx := context.Background()

	s, err := oc.Resolve(ctx, "ses_ok", "")
	if err != nil || s.Directory != dir {
		t.Fatalf("worktree should come from the session: %+v %v", s, err)
	}
	for id, want := range map[string]string{"ses_old": "archived", "ses_away": "works in /elsewhere", "ses_none": "not found", "bad id": "not an opencode session"} {
		if _, err := oc.Resolve(ctx, id, dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", id, want, err)
		}
	}
	if _, err := oc.Resolve(ctx, "ses_ok", "relative/path"); err == nil {
		t.Error("relative worktree must be rejected")
	}

	id, err := oc.Prompt(ctx, "ses_ok", dir, "hello", "task:7")
	if err != nil || id != "inbox_1" {
		t.Fatalf("prompt: %q %v", id, err)
	}
	last := f.calls[len(f.calls)-1]
	if last[0] != dir || last[1] != "api" || last[2] != "post" || last[3] != "/api/session/ses_ok/prompt" {
		t.Fatalf("prompt call: %v", last)
	}
	var body map[string]any
	json.Unmarshal([]byte(last[5]), &body)
	if body["text"] != "hello" || body["delivery"] != "queue" || body["resume"] != true {
		t.Fatalf("prompt body: %v", body)
	}

	f.prompt = `{}`
	if _, err := oc.Prompt(ctx, "ses_ok", dir, "hello", "k"); err == nil || !strings.Contains(err.Error(), "no inbox ID") {
		t.Fatalf("missing inbox id must be an error: %v", err)
	}
}
