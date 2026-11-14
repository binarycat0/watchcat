package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"
)

// Settings are the config values that can change while the daemon runs
// (from the web UI's settings page). Everything else needs a restart.
type Settings struct {
	Workflows    map[string]string `json:"workflows"`
	PollInterval Duration          `json:"poll_interval"`
	NotifyOn     []string          `json:"notify_on"`
	// Provider settings, prefixed with the provider's config key.
	GitHubMergeMethod string `json:"github_merge_method"`
	SlackUserID       string `json:"slack_user_id"`
	SlackChannel      string `json:"slack_channel"`
}

// Validation sets for Settings.
var (
	Modes        = []string{"off", "dry-run", "on"}
	MergeMethods = []string{"squash", "merge", "rebase"}
)

// Validate rejects values the daemon can't act on.
func (s Settings) Validate(ruleNames []string) error {
	for name, mode := range s.Workflows {
		if !slices.Contains(ruleNames, name) {
			return fmt.Errorf("unknown workflow %q", name)
		}
		if !slices.Contains(Modes, mode) {
			return fmt.Errorf("workflow %s: mode must be off, dry-run or on", name)
		}
	}
	if !slices.Contains(MergeMethods, s.GitHubMergeMethod) {
		return errors.New("github merge_method must be squash, merge or rebase")
	}
	if s.PollInterval.Duration < time.Minute {
		return errors.New("poll_interval must be at least 1m")
	}
	return nil
}

// Live holds the running daemon's config and lets the settings page change
// parts of it. Readers call Get() each time instead of keeping a copy, so a
// change applies from the next check on.
type Live struct {
	mu    sync.RWMutex // many readers, one writer
	cfg   Config
	paths Paths
}

func NewLive(cfg Config, p Paths) *Live { return &Live{cfg: cfg, paths: p} }

// ConfigPath is the file Update writes to.
func (l *Live) ConfigPath() string { return l.paths.Config }

// Get returns a copy of the current config.
func (l *Live) Get() Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	c := l.cfg
	c.Workflows = make(map[string]string, len(l.cfg.Workflows))
	for k, v := range l.cfg.Workflows {
		c.Workflows[k] = v
	}
	c.NotifyOn = slices.Clone(l.cfg.NotifyOn)
	return c
}

// Settings returns the editable part of the config.
func (l *Live) Settings() Settings {
	c := l.Get()
	return Settings{
		Workflows: c.Workflows, GitHubMergeMethod: c.GitHub.MergeMethod, PollInterval: c.PollInterval,
		NotifyOn: c.NotifyOn, SlackUserID: c.Slack.UserID, SlackChannel: c.Slack.Channel,
	}
}

// Update applies new settings in memory and writes them to config.json.
func (l *Live) Update(s Settings) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := saveSettings(l.paths, s); err != nil {
		return err
	}
	for k, v := range s.Workflows {
		l.cfg.Workflows[k] = v
	}
	l.cfg.GitHub.MergeMethod = s.GitHubMergeMethod
	l.cfg.PollInterval = s.PollInterval
	l.cfg.NotifyOn = slices.Clone(s.NotifyOn)
	l.cfg.Slack.UserID = s.SlackUserID
	l.cfg.Slack.Channel = s.SlackChannel
	return nil
}

// saveSettings patches only the editable keys into config.json. It never
// writes the whole Config: the daemon's in-memory copy holds the Slack token
// it read from the keychain, which must not end up in the file.
func saveSettings(p Paths, s Settings) error {
	raw := map[string]any{}
	if b, err := os.ReadFile(p.Config); err == nil {
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", p.Config, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	wf, _ := raw["workflows"].(map[string]any)
	if wf == nil {
		wf = map[string]any{}
	}
	for k, v := range s.Workflows {
		wf[k] = v
	}
	raw["workflows"] = wf
	raw["poll_interval"] = s.PollInterval.String()
	raw["notify_on"] = s.NotifyOn
	section(raw, "github")["merge_method"] = s.GitHubMergeMethod
	slack := section(raw, "slack")
	slack["user_id"] = s.SlackUserID
	slack["channel"] = s.SlackChannel

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	// Write to a temp file and rename, so a crash never leaves half a config.
	tmp := p.Config + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.Config)
}

// section returns raw[key] as an object, creating it if missing, so a
// provider's other keys in config.json are kept.
func section(raw map[string]any, key string) map[string]any {
	m, _ := raw[key].(map[string]any)
	if m == nil {
		m = map[string]any{}
		raw[key] = m
	}
	return m
}
