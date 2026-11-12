package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/binarycat0/watchcat/internal/api"
	"github.com/binarycat0/watchcat/internal/config"
	"github.com/binarycat0/watchcat/internal/core"
	"github.com/binarycat0/watchcat/internal/health"
	"github.com/binarycat0/watchcat/internal/secrets"
	"github.com/binarycat0/watchcat/internal/service"
	"github.com/binarycat0/watchcat/internal/slack"
)

// healthChecks builds the tool checks from config and the watched PRs. The
// daemon passes its live config so the report reflects settings changes.
func (e env) healthChecks(cfg config.Config, prs []core.PR) []health.Check {
	repos := make([]string, len(prs))
	for i, p := range prs {
		repos[i] = p.Key().Slug()
	}
	var bot *slack.Bot
	if cfg.Slack.BotToken != "" {
		bot = &slack.Bot{Token: cfg.Slack.BotToken}
	}
	ex := health.OSExec{}
	return []health.Check{
		e.setupCheck(),
		health.GH{Exec: ex, Repos: repos},
		health.SlackBot{Bot: bot, Channel: cfg.Slack.Target(), TokenSource: e.tokenSource, StoreErr: e.tokenErr},
		health.SlackCLI{Exec: ex},
		health.Opencode{Exec: ex},
	}
}

// setupCheck describes watchcat's own install, with secrets re-read from
// the keychain on every run so a newly set token shows up immediately.
func (e env) setupCheck() health.Setup {
	home, _ := os.UserHomeDir()
	return health.Setup{
		Bin:      e.paths.Bin,
		Link:     filepath.Join(home, ".local", "bin", "watchcat"),
		Service:  serviceStatus,
		Secrets:  e.secretStatuses(),
		LegacyIn: config.LegacyDir(),

		OtherWatchers: otherWatchers(),
	}
}

// legacyLabel is the launchd job of the earlier Python pr-watchdog. It sends
// prompts to opencode sessions and merges PRs on its own schedule, which
// would look like watchcat ignoring its dry-run settings.
const legacyLabel = "local.pr-watchdog"

func otherWatchers() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), legacyLabel)
	if exec.Command("launchctl", "print", target).Run() != nil {
		return nil
	}
	return []string{"the Python pr-watchdog (" + legacyLabel + ") is still loaded and sends prompts / merges on its own; " +
		"stop it with: launchctl bootout " + target}
}

func serviceStatus() (string, string, error) {
	m, err := service.ForPlatform()
	if err != nil {
		return "", "", err
	}
	s, err := m.Status()
	return m.Name(), s, err
}

// secretStatuses resolves every known secret and masks its value.
func (e env) secretStatuses() []health.SecretStatus {
	fileCfg, _ := config.Load(e.paths) // only the file, for a legacy plaintext token
	var out []health.SecretStatus
	for _, s := range secrets.Known {
		legacy := ""
		if s.Name == secrets.SlackToken {
			legacy = fileCfg.Slack.BotToken
		}
		v, src, err := secrets.Resolve(secrets.Keyring{}, s, legacy)
		st := health.SecretStatus{Name: s.Name, Source: src, Hint: "watchcat secret set " + s.Name}
		if v != "" {
			st.Masked = secrets.Mask(v)
		}
		if err != nil {
			st.Err = err.Error()
		}
		out = append(out, st)
	}
	return out
}

// localHealth runs the checks in this process. It reads the watched repos
// straight from the database, which is safe because the daemon isn't running.
func (e env) localHealth(ctx context.Context) health.Report {
	var prs []core.PR
	if _, err := os.Stat(e.paths.DB); err == nil {
		if store, err := core.Open(e.paths.DB); err == nil {
			prs, _ = store.ListPRs(ctx)
			store.Close()
		}
	}
	return health.Run(ctx, e.healthChecks(e.cfg, prs)...)
}

func cmdHealth(e env, args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	local := fs.Bool("local", false, "run checks in this shell instead of asking the daemon")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	refresh := fs.Bool("refresh", false, "ask the daemon to re-run checks instead of using its cached report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var rep health.Report
	source := "daemon"
	err := api.ErrDaemonDown
	if !*local {
		rep, err = e.client().Health(ctx, *refresh)
	}
	if errors.Is(err, api.ErrDaemonDown) {
		source = "this shell"
		if !*local {
			source += " (daemon not running)"
		}
		rep, err = e.localHealth(ctx), nil
	}
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Printf("tool health: %s (checked from %s)\n\n", rep.Status, source)
	printReport(rep)
	if rep.Status == health.Fail {
		return fmt.Errorf("required tools are not ready")
	}
	return nil
}

func printReport(rep health.Report) {
	for _, r := range rep.Results {
		req := "optional"
		if r.Required {
			req = "required"
		}
		title := r.Title
		if r.Version != "" {
			title += " " + r.Version
		}
		fmt.Printf("[%-4s] %s (%s): %s\n", r.Status, title, req, r.Summary)
		for _, d := range r.Details {
			mark := " "
			if d.Status == health.Fail {
				mark = "!"
			}
			fmt.Printf("     %s %-34s %s\n", mark, d.Label, d.Value)
		}
	}
}
