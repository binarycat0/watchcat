// Package config resolves where watchcat keeps its files and loads the
// user-editable config.json.
//
// Python analogy: this is the module you'd put in settings.py. Go has no
// implicit globals-on-import, so callers ask for a Config explicitly.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the on-disk configuration. The struct tags (`json:"..."`) tell
// encoding/json which keys map to which fields, like a pydantic model.
type Config struct {
	// Host and Port are where the daemon serves the API and web UI. Keep
	// Host on loopback: the API has no auth.
	Host string `json:"host"`
	Port int    `json:"port"`
	// PollInterval is how often each PR is checked, e.g. "5m".
	PollInterval Duration `json:"poll_interval"`
	// TickInterval is how often the worker pool looks for due work.
	TickInterval Duration `json:"tick_interval"`
	// Workers is how many background tasks may run at once.
	Workers int `json:"workers"`
	// NotifyOn lists the PR change kinds that send a Slack message.
	NotifyOn []string `json:"notify_on"`
	Slack    Slack    `json:"slack"`
	// Workflows sets each workflow rule to "off", "dry-run" or "on".
	// dry-run does everything except the action itself and records what it
	// would have done.
	Workflows map[string]string `json:"workflows"`
	// MergeMethod is used for merges and auto-merge: squash, merge or rebase.
	MergeMethod string `json:"merge_method"`
	// GitHubHost is assumed for short refs like owner/repo#123. URLs and
	// host/owner/repo#123 refs carry their own host (e.g. GitHub Enterprise).
	GitHubHost string `json:"github_host"`
}

// Slack holds bot settings. The token itself lives in the OS keychain
// (`watchcat secret set slack-token`); BotToken is only read from older
// config files and filled in at startup from wherever the token was found.
type Slack struct {
	BotToken string `json:"bot_token,omitempty"`
	// UserID (U...) receives notifications as DMs from the bot.
	UserID string `json:"user_id"`
	// Channel is used instead when UserID is empty: a channel ID the bot
	// was invited to.
	Channel string `json:"channel"`
}

// Target is where notifications go: the user's DM if set, else the channel.
func (s Slack) Target() string {
	if s.UserID != "" {
		return s.UserID
	}
	return s.Channel
}

// Enabled reports whether enough is set to send messages.
func (s Slack) Enabled() bool { return s.BotToken != "" && s.Target() != "" }

// Duration wraps time.Duration so it reads/writes as "5m" in JSON instead of
// nanoseconds.
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// String drops Go's trailing zero units: "5m" rather than "5m0s".
func (d Duration) String() string {
	s := d.Duration.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Addr is Host:Port, the address the daemon listens on and clients dial.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

func Default() Config {
	return Config{
		Host:         "127.0.0.1",
		Port:         7878,
		PollInterval: Duration{5 * time.Minute},
		TickInterval: Duration{15 * time.Second},
		Workers:      4,
		// Every change kind except "pushed", which your agent does constantly.
		NotifyOn: []string{"ci_failed", "ci_passed", "conflict", "conflict_resolved",
			"changes_requested", "approved", "new_comments", "merged", "closed"},
		// Everything starts in dry-run until you switch it on.
		Workflows: map[string]string{
			"merge": "dry-run", "review_threads": "dry-run", "ci_failed": "dry-run", "needs_review": "dry-run",
		},
		MergeMethod: "squash",
		GitHubHost:  "github.com",
	}
}

// Paths are the files watchcat owns. Everything lives under one directory,
// ~/.watchcat by default:
//
//	~/.watchcat/bin/watchcat     the binary (~/.local/bin/watchcat links here)
//	~/.watchcat/config.json      settings, including the Slack token (0600)
//	~/.watchcat/watchcat.db      SQLite: PRs, history, task queue
//	~/.watchcat/logs/watchcat.log daemon output when run as a service
type Paths struct {
	Dir    string
	Bin    string // the installed binary
	Config string
	DB     string
	Log    string
}

// DefaultPaths honours $WATCHCAT_HOME, otherwise uses ~/.watchcat.
func DefaultPaths() (Paths, error) {
	dir := os.Getenv("WATCHCAT_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".watchcat")
	}
	return PathsIn(dir), nil
}

func PathsIn(dir string) Paths {
	return Paths{
		Dir:    dir,
		Bin:    filepath.Join(dir, "bin", "watchcat"),
		Config: filepath.Join(dir, "config.json"),
		DB:     filepath.Join(dir, "watchcat.db"),
		Log:    filepath.Join(dir, "logs", "watchcat.log"),
	}
}

// Load reads config.json, falling back to defaults for a missing file or
// missing keys.
func Load(p Paths) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(p.Config)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	// Unmarshalling into a pre-filled struct only overwrites keys present in
	// the file, so defaults survive.
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", p.Config, err)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("%s: port %d out of range 1-65535", p.Config, cfg.Port)
	}
	return cfg, nil
}

// RemovePlaintextToken deletes slack.bot_token from config.json, keeping
// every other key exactly as the user wrote it. It reports whether there was
// one to remove.
func RemovePlaintextToken(p Paths) (bool, error) {
	b, err := os.ReadFile(p.Config)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A generic map (like a Python dict) round-trips keys we don't model.
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", p.Config, err)
	}
	slack, _ := raw["slack"].(map[string]any)
	if _, ok := slack["bot_token"]; !ok {
		return false, nil
	}
	delete(slack, "bot_token")
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(p.Config, append(out, '\n'), 0o600)
}

// Init creates the directory and writes a default config.json if none exists.
// It reports whether a new config was written.
func Init(p Paths) (bool, error) {
	// 0700: config.json holds the Slack token.
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Log), 0o700); err != nil {
		return false, err
	}
	if _, err := os.Stat(p.Config); err == nil {
		return false, nil
	}
	b, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(p.Config, append(b, '\n'), 0o600)
}
