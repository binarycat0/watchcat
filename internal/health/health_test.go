package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/binarycat0/watchcat/internal/slack"
)

// fakeExec answers commands from a map keyed by the joined argv.
type fakeExec struct {
	installed map[string]bool
	out       map[string]string
	errs      map[string]string
}

func (f fakeExec) LookPath(name string) (string, error) {
	if f.installed[name] {
		return "/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (f fakeExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	for k, v := range f.errs {
		if strings.HasPrefix(key, k) {
			return []byte(f.out[k]), errors.New(v)
		}
	}
	for k, v := range f.out {
		if strings.HasPrefix(key, k) {
			return []byte(v), nil
		}
	}
	return nil, errors.New("unexpected command: " + key)
}

const authOK = `{"hosts":{"github.com":[
 {"state":"success","active":true,"host":"github.com","login":"me-work","tokenSource":"keyring","scopes":"read:org, repo"},
 {"state":"failed","active":false,"host":"github.com","login":"me-old","error":"token expired"}],
 "ghe.corp":[{"state":"success","active":true,"host":"ghe.corp","login":"I123","tokenSource":"keyring","scopes":"repo"}]}}`

func TestGH(t *testing.T) {
	ex := fakeExec{
		installed: map[string]bool{"gh": true},
		out: map[string]string{
			"gh --version":                                     "gh version 2.92.0 (2026-04-28)\nhttps://...",
			"gh auth status":                                   authOK,
			"gh api --hostname github.com user/orgs":           "dremio\napache\n",
			"gh api --hostname ghe.corp user/orgs":             "corp\n",
			"gh api --hostname github.com repos/dremio/dremio": "admin,pull,push",
			"gh api --hostname ghe.corp repos/corp/app":        "pull,push",
		},
		errs: map[string]string{"gh api --hostname github.com repos/secret/repo": "exit status 1: HTTP 404: Not Found"},
	}
	r := GH{Exec: ex, Repos: []string{"github.com/dremio/dremio", "github.com/dremio/dremio", "ghe.corp/corp/app"}}.Run(context.Background())
	if r.Status != OK || r.Version != "2.92.0 (2026-04-28)" || !strings.Contains(r.Summary, "signed in to ghe.corp, github.com") {
		t.Fatalf("%+v", r)
	}
	if !hasDetail(r, "account ghe.corp", OK) || !hasDetail(r, "account github.com", Fail) || !hasDetail(r, "repo ghe.corp/corp/app", OK) {
		t.Fatalf("details: %+v", r.Details)
	}

	r = GH{Exec: ex, Repos: []string{"github.com/secret/repo", "other.host/x/y"}}.Run(context.Background())
	if r.Status != Fail || !hasDetail(r, "repo github.com/secret/repo", Fail) || !hasDetail(r, "repo other.host/x/y", Fail) {
		t.Fatalf("unreadable repo / unknown host should fail: %+v", r)
	}

	ex.out["gh auth status"] = strings.Replace(authOK, "read:org, repo", "read:org", 1)
	if r = (GH{Exec: ex}).Run(context.Background()); r.Status != Warn {
		t.Fatalf("missing repo scope should warn: %+v", r)
	}

	if r = (GH{Exec: fakeExec{}}).Run(context.Background()); r.Status != Fail {
		t.Fatalf("missing gh should fail: %+v", r)
	}
}

func hasDetail(r Result, label string, s Status) bool {
	for _, d := range r.Details {
		if d.Label == label && d.Status == s {
			return true
		}
	}
	return false
}

func TestSlackBot(t *testing.T) {
	scopes := "chat:write,users:read"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		w.Header().Set("X-OAuth-Scopes", scopes)
		w.Write([]byte(`{"ok":true,"team":"Dremio","url":"https://dremio.slack.com/","user":"watchcat","user_id":"U1","bot_id":"B1"}`))
	}))
	defer ts.Close()
	bot := func(tok string) *slack.Bot { return &slack.Bot{Token: tok, BaseURL: ts.URL} }

	r := SlackBot{Bot: bot("good"), Channel: "C1"}.Run(context.Background())
	if r.Status != OK || r.Summary != "authenticated as watchcat in Dremio" {
		t.Fatalf("%+v", r)
	}
	if r = (SlackBot{Bot: bot("bad"), Channel: "C1"}).Run(context.Background()); r.Status != Fail || !strings.Contains(r.Summary, "invalid_auth") {
		t.Fatalf("%+v", r)
	}
	if r = (SlackBot{Bot: bot("good")}).Run(context.Background()); r.Status != Fail {
		t.Fatalf("missing channel should fail: %+v", r)
	}
	scopes = "users:read"
	if r = (SlackBot{Bot: bot("good"), Channel: "C1"}).Run(context.Background()); r.Status != Fail || !strings.Contains(r.Summary, "chat:write") {
		t.Fatalf("missing scope should fail: %+v", r)
	}
	if r = (SlackBot{}).Run(context.Background()); r.Status != Fail {
		t.Fatalf("no token should fail: %+v", r)
	}
}

func TestSlackCLI(t *testing.T) {
	if r := (SlackCLI{Exec: fakeExec{}}).Run(context.Background()); r.Status != Skip {
		t.Fatalf("%+v", r)
	}
	ex := fakeExec{installed: map[string]bool{"slack": true}, out: map[string]string{
		"slack version": "Using slack v3.1.0", "slack auth list": "dremio (Team ID: T1)\nUser ID: U1\n"}}
	if r := (SlackCLI{Exec: ex}).Run(context.Background()); r.Status != OK || len(r.Details) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestOpencode(t *testing.T) {
	ex := fakeExec{installed: map[string]bool{"opencode": true}, out: map[string]string{"opencode --version": "1.4.2"}}
	if r := (Opencode{Exec: ex}).Run(context.Background()); r.Status != OK || r.Version != "1.4.2" {
		t.Fatalf("%+v", r)
	}
	if r := (Opencode{Exec: fakeExec{}}).Run(context.Background()); r.Status != Fail || r.Required {
		t.Fatalf("missing opencode is an optional failure: %+v", r)
	}
}

type fixed Result

func (f fixed) Run(context.Context) Result { return Result(f) }

func TestRunOverall(t *testing.T) {
	ok := fixed{Status: OK, Required: true}
	optFail := fixed{Status: Fail, Required: false}
	reqFail := fixed{Status: Fail, Required: true}
	if Run(context.Background(), ok, optFail).Status != Warn {
		t.Fatal("optional failure should only warn")
	}
	if Run(context.Background(), ok, optFail, reqFail).Status != Fail {
		t.Fatal("required failure should fail")
	}
}

func TestSetup(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/bin/watchcat"
	link := dir + "/link/watchcat"
	os.MkdirAll(dir+"/bin", 0o755)
	os.MkdirAll(dir+"/link", 0o755)
	os.WriteFile(bin, []byte("x"), 0o755)
	os.Symlink(bin, link)
	t.Setenv("PATH", dir+"/link:/usr/bin")

	cfg := dir + "/config.json"
	os.WriteFile(cfg, []byte("{}"), 0o644)
	svc := func() (string, string, error) { return "launchd", "loaded", nil }
	up := func(context.Context) (string, error) { return "pid 1", nil }
	down := func(context.Context) (string, error) { return "", errors.New("connection refused") }
	ok := Setup{Config: cfg, DB: cfg, Bin: bin, Link: link, Service: svc, Daemon: up,
		Secrets: []SecretStatus{{Name: "slack-token", Source: "keychain", Masked: "xoxb****abcd"}}}.Run(context.Background())
	if ok.Status != OK || !strings.Contains(fmt.Sprint(ok.Details), "keychain xoxb****abcd") {
		t.Fatalf("%+v", ok)
	}

	bad := Setup{Config: dir + "/none.json", DB: dir + "/none.db", Bin: bin, Link: dir + "/nope", Service: svc, Daemon: down,
		Secrets: []SecretStatus{{Name: "slack-token", Source: "config.json (plaintext)", Masked: "xoxb****abcd", Hint: "watchcat secret set slack-token"}}}.Run(context.Background())
	if bad.Status != Warn || !hasDetail(bad, "symlink", Fail) || !hasDetail(bad, "secret slack-token", Fail) ||
		!hasDetail(bad, "config", Fail) || !hasDetail(bad, "database", Fail) || !hasDetail(bad, "daemon", Fail) {
		t.Fatalf("%+v", bad)
	}
}
