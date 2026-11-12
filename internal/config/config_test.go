package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovePlaintextToken(t *testing.T) {
	p := PathsIn(t.TempDir())
	os.WriteFile(p.Config, []byte(`{"listen":"127.0.0.1:1","custom":1,"slack":{"bot_token":"xoxb-x","user_id":"U1"}}`), 0o600)

	removed, err := RemovePlaintextToken(p)
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	b, _ := os.ReadFile(p.Config)
	if strings.Contains(string(b), "xoxb") || !strings.Contains(string(b), `"custom": 1`) || !strings.Contains(string(b), `"user_id": "U1"`) {
		t.Fatalf("token should be gone and other keys kept:\n%s", b)
	}
	if info, _ := os.Stat(p.Config); info.Mode().Perm() != 0o600 {
		t.Fatalf("config must stay 0600, got %v", info.Mode().Perm())
	}
	if removed, _ := RemovePlaintextToken(p); removed {
		t.Fatal("second call has nothing to remove")
	}
}

func TestDefaultConfigHasNoToken(t *testing.T) {
	p := PathsIn(t.TempDir())
	if _, err := Init(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.Config)
	if strings.Contains(string(b), "bot_token") {
		t.Fatalf("new configs must not have a token field:\n%s", b)
	}
}

func TestLiveUpdateNeverWritesToken(t *testing.T) {
	p := PathsIn(t.TempDir())
	Init(p)
	cfg, _ := Load(p)
	cfg.Slack.BotToken = "xoxb-from-keychain-1234" // what the daemon holds in memory
	live := NewLive(cfg, p)

	s := live.Settings()
	s.Workflows["merge"] = "on"
	s.MergeMethod = "rebase"
	s.SlackUserID = "U9"
	if err := s.Validate([]string{"merge", "review_threads", "ci_failed", "needs_review"}); err != nil {
		t.Fatal(err)
	}
	if err := live.Update(s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.Config)
	if strings.Contains(string(b), "xoxb") {
		t.Fatalf("token leaked into config.json:\n%s", b)
	}
	reloaded, _ := Load(p)
	if reloaded.Workflows["merge"] != "on" || reloaded.MergeMethod != "rebase" || reloaded.Slack.UserID != "U9" {
		t.Fatalf("settings not persisted: %+v", reloaded)
	}
	if got := live.Get(); got.Workflows["merge"] != "on" || got.Slack.BotToken == "" {
		t.Fatalf("in-memory config: %+v", got)
	}
}

func TestSettingsValidate(t *testing.T) {
	rules := []string{"merge"}
	good := Settings{Workflows: map[string]string{"merge": "dry-run"}, MergeMethod: "squash", PollInterval: Duration{5 * 60e9}, GitHubHost: "github.com"}
	if err := good.Validate(rules); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Settings{
		{Workflows: map[string]string{"merge": "yes"}, MergeMethod: "squash", PollInterval: good.PollInterval},
		{Workflows: map[string]string{"nope": "on"}, MergeMethod: "squash", PollInterval: good.PollInterval},
		{MergeMethod: "fast-forward", PollInterval: good.PollInterval},
		{MergeMethod: "squash", PollInterval: Duration{1e9}, GitHubHost: "github.com"},
		{MergeMethod: "squash", PollInterval: good.PollInterval, GitHubHost: "https://ghe.corp"},
	} {
		if bad.Validate(rules) == nil {
			t.Errorf("should reject %+v", bad)
		}
	}
}

func TestDurationString(t *testing.T) {
	for in, want := range map[string]string{"5m": "5m", "90s": "1m30s", "2h": "2h", "1h30m": "1h30m"} {
		var d Duration
		d.UnmarshalJSON([]byte(`"` + in + `"`))
		if d.String() != want {
			t.Errorf("%s -> %s, want %s", in, d.String(), want)
		}
	}
}

func TestMigrateFromWatchdogDir(t *testing.T) {
	old := t.TempDir()
	os.WriteFile(filepath.Join(old, "config.json"), []byte(`{}`), 0o600)
	os.WriteFile(filepath.Join(old, "watchdog.db"), []byte("db"), 0o600)
	p := PathsIn(filepath.Join(t.TempDir(), ".watchcat"))

	moved, err := migrateFrom(old, p)
	if err != nil || moved != old {
		t.Fatalf("moved=%q err=%v", moved, err)
	}
	if b, err := os.ReadFile(p.DB); err != nil || string(b) != "db" {
		t.Fatalf("db not moved to %s: %q %v", p.DB, b, err)
	}
	if _, err := os.Stat(p.Config); err != nil {
		t.Fatalf("config not moved: %v", err)
	}
	// Already set up: a second run leaves things alone.
	if moved, _ := migrateFrom(old, p); moved != "" {
		t.Fatalf("second migrate moved from %q", moved)
	}
}
