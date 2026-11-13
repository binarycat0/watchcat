// Command watchcat is both the daemon and its CLI client.
//
//	watchcat daemon           run scheduler + API + web UI (what the service runs)
//	watchcat pr add <ref>     talk to the running daemon over its localhost API
//	watchcat service install  set up launchd/systemd (works without the daemon)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/binarycat0/watchcat/internal/api"
	"github.com/binarycat0/watchcat/internal/config"
	"github.com/binarycat0/watchcat/internal/secrets"
)

// version is overridden at build time: go build -ldflags "-X main.version=1.2.3"
var version = "dev"

const usage = `watchcat - keeps an eye on your pull requests

Usage:
  watchcat init                     create config and data directory
  watchcat daemon                   run the daemon in the foreground
  watchcat status                   show daemon status
  watchcat pr add <ref>             watch a PR: URL (any GitHub host), owner/repo#123 or host/owner/repo#123
  watchcat pr list                  list watched PRs
  watchcat pr show <id|ref>         show a PR and its history
  watchcat pr remove <id|ref>       stop watching a PR
  watchcat pr check <id|ref>        check a PR now
  watchcat pr retrigger <id|ref>    re-run failed CI jobs
  watchcat pr bind <id|ref> --session ses_... [--worktree /path]
                                    link a PR to the opencode session working on it (unbind to remove)
  watchcat report [--days N]        PR lifecycle stats: counts, time to merge, CI failures (p50/p95)
  watchcat mcp                      MCP server on stdio for AI agents (add to opencode's config)
  watchcat agent sessions           list open opencode sessions and their worktrees
  watchcat pr prompt-ci|prompt-review <id|ref>
                                    send the CI-failure or review-threads prompt to the PR's session now
  watchcat pr pause|resume <id|ref> pause or resume checks
  watchcat tasks [--status S] [--pr id|ref]
                                    list background tasks (queued/running/done/failed)
  watchcat slack test               send a test message with the configured bot
  watchcat secret set|delete <name>  store a secret in the OS keychain (prompted, never on the command line)
  watchcat secret list              show which secrets are set and where they come from
  watchcat service install|uninstall|start|stop|restart|status
  watchcat health [--local] [--json] [--refresh]
                                    check the setup, daemon and tools (gh, Slack, opencode);
                                    asks the daemon, --local checks from this shell
  watchcat version

Environment:
  WATCHCAT_HOME         override the config/data directory
  WATCHCAT_SLACK_TOKEN  Slack bot token, overrides the keychain
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
			fmt.Fprintln(os.Stderr, "start it with `watchcat service start` or `watchcat daemon`")
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
	case "daemon", "health":
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
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (see `watchcat help`)", cmd)
}

func (e env) client() *api.Client { return api.NewClient(e.cfg.Addr()) }

// ctx gives each CLI call a deadline so a hung daemon can't hang the shell.
func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func cmdInit(e env) error {
	created, err := config.Init(e.paths)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("wrote", e.paths.Config)
	} else {
		fmt.Println("config already exists:", e.paths.Config)
	}
	fmt.Println("next: `watchcat service install && watchcat service start`")
	return nil
}
