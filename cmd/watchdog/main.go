// Command watchdog is both the daemon and its CLI client.
//
//	watchdog daemon           run scheduler + API + web UI (what the service runs)
//	watchdog pr add <ref>     talk to the running daemon over its localhost API
//	watchdog service install  set up launchd/systemd (works without the daemon)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/binarycat0/watchdog/internal/api"
	"github.com/binarycat0/watchdog/internal/config"
	"github.com/binarycat0/watchdog/internal/secrets"
)

// version is overridden at build time: go build -ldflags "-X main.version=1.2.3"
var version = "dev"

const usage = `watchdog - keeps an eye on your pull requests

Usage:
  watchdog init                     create config and data directory
  watchdog daemon                   run the daemon in the foreground
  watchdog status                   show daemon status
  watchdog pr add <ref>             watch a PR: URL (any GitHub host), owner/repo#123 or host/owner/repo#123
  watchdog pr list                  list watched PRs
  watchdog pr show <id|ref>         show a PR and its history
  watchdog pr remove <id|ref>       stop watching a PR
  watchdog pr check <id|ref>        check a PR now
  watchdog pr retrigger <id|ref>    re-run failed CI jobs
  watchdog pr bind <id|ref> --session ses_... [--worktree /path]
                                    link a PR to the opencode session working on it (unbind to remove)
  watchdog report [--days N]        PR lifecycle stats: counts, time to merge, CI failures (p50/p95)
  watchdog mcp                      MCP server on stdio for AI agents (add to opencode's config)
  watchdog agent sessions           list open opencode sessions and their worktrees
  watchdog pr prompt-ci|prompt-review <id|ref>
                                    send the CI-failure or review-threads prompt to the PR's session now
  watchdog pr pause|resume <id|ref> pause or resume checks
  watchdog tasks [--status S] [--pr id|ref]
                                    list background tasks (queued/running/done/failed)
  watchdog slack test               send a test message with the configured bot
  watchdog secret set|delete <name>  store a secret in the OS keychain (prompted, never on the command line)
  watchdog secret list              show which secrets are set and where they come from
  watchdog service install|uninstall|start|stop|restart|status
  watchdog health [--local] [--json] [--refresh]
                                    check gh, Slack and opencode (as the daemon sees them)
  watchdog doctor                   diagnose the setup
  watchdog version

Environment:
  WATCHDOG_HOME         override the config/data directory
  WATCHDOG_SLACK_TOKEN  Slack bot token, overrides the keychain
`

// env bundles what every command needs. Passing it explicitly (instead of
// package-level globals) keeps commands easy to test.
type env struct {
	paths config.Paths
	cfg   config.Config
	// tokenSource says where the Slack token came from (see secrets.Resolve);
	// tokenErr is a secret-store error, e.g. a locked keychain.
	tokenSource string
	tokenErr    error
}

// loadSecrets fills in the Slack token. Only commands that use it call this,
// so plain CLI commands never touch the keychain.
func (e *env) loadSecrets() {
	s, _ := secrets.Lookup(secrets.SlackToken)
	e.cfg.Slack.BotToken, e.tokenSource, e.tokenErr = secrets.Resolve(secrets.Keyring{}, s, e.cfg.Slack.BotToken)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, api.ErrDaemonDown) {
			fmt.Fprintln(os.Stderr, "start it with `watchdog service start` or `watchdog daemon`")
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	e := env{paths: paths, cfg: cfg}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "daemon", "health", "doctor":
		e.loadSecrets()
	}
	switch cmd {
	case "init":
		return cmdInit(e)
	case "daemon":
		return cmdDaemon(e, rest)
	case "status":
		return cmdStatus(e)
	case "pr":
		return cmdPR(e, rest)
	case "tasks":
		return cmdTasks(e, rest)
	case "slack":
		return cmdSlack(e, rest)
	case "service":
		return cmdService(e, rest)
	case "mcp":
		return cmdMCP(e, rest)
	case "report":
		return cmdReport(e, rest)
	case "agent":
		return cmdAgent(e, rest)
	case "secret":
		return cmdSecret(e, rest)
	case "health":
		return cmdHealth(e, rest)
	case "doctor":
		return cmdDoctor(e)
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (see `watchdog help`)", cmd)
}

func (e env) client() *api.Client { return api.NewClient(e.cfg.Listen) }

// ctx gives each CLI call a deadline so a hung daemon can't hang the shell.
func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func cmdInit(e env) error {
	// Move data from the pre-~/.watchdog location, but never under a
	// running daemon: it holds the database open.
	if old := config.LegacyDir(); old != "" && os.Getenv("WATCHDOG_HOME") == "" {
		c, cancel := ctx()
		_, err := e.client().Status(c)
		cancel()
		if err == nil {
			return fmt.Errorf("found old data in %s; stop the daemon (`watchdog service stop`) and run init again to move it", old)
		}
		moved, err := config.MigrateLegacy(e.paths)
		if err != nil {
			return err
		}
		if moved != "" {
			fmt.Printf("moved config and database from %s to %s\n", moved, e.paths.Dir)
		}
	}
	created, err := config.Init(e.paths)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("wrote", e.paths.Config)
	} else {
		fmt.Println("config already exists:", e.paths.Config)
	}
	fmt.Println("next: `watchdog service install && watchdog service start`")
	return nil
}
