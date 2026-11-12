# watchcat

A personal daemon that watches your pull requests (CI, reviews, conflicts),
notifies Slack, and hands work to the opencode/Claude session that owns each PR.

One binary does everything:

- `watchcat daemon` runs the scheduler, the SQLite store, the localhost API and
  the web UI in a single process. This is what launchd/systemd keep alive.
- Every other command (`pr add`, `status`, ...) is a client of that API.
- `init`, `service ...` and `doctor` work without the daemon.

## Prerequisites

| What | Why | Check / install |
|---|---|---|
| macOS (launchd) or Linux (systemd --user) | runs the daemon as a background service | |
| Go 1.26+ | builds the binary | `go version` · `brew install go` |
| Node.js 20+ and npm | builds the web UI (only at build time; the binary doesn't need it) | `node --version` · `brew install node` |
| GitHub CLI `gh`, signed in with the `repo` scope | every PR check, merge and rerun goes through it | `gh auth status` · `gh auth login` |
| …plus each GitHub Enterprise host you use | PRs on a host gh isn't signed in to are refused | `gh auth login --hostname github.example.com` |
| `~/.local/bin` on `PATH` | `make install` links `watchcat` there | add `export PATH="$HOME/.local/bin:$PATH"` to `~/.zshrc` |
| Slack app bot token with `chat:write` (optional) | DMs about PR changes and rule actions | `watchcat secret set slack-token` (stored in the Keychain) |
| opencode (optional) | agent hand-off: prompts to the session working on a PR | `opencode --version` |

On Linux, storing secrets needs `secret-tool` (libsecret) and a running
keyring; otherwise use the `WATCHCAT_SLACK_TOKEN` environment variable.
`watchcat doctor` and the Health page check all of this.

## Quick start

```bash
make install                 # builds the UI (npm) and binary -> ~/.watchcat/bin, symlink in ~/.local/bin
watchcat init                # writes ~/.watchcat/config.json
watchcat service install     # launchd agent on macOS, systemd --user unit on Linux
watchcat service start
watchcat pr add dremio/dremio#123
open http://127.0.0.1:7878   # web UI
```

`~/.local/bin` must be on your `PATH` (`make install` tells you if it isn't).
To upgrade, pull and run `make install` again: it swaps the binary atomically
and restarts the service if it's running. `make uninstall` removes the
binary, symlink and service but keeps `~/.watchcat`.

The service runs `~/.watchcat/bin/watchcat` directly, not the symlink, so it
keeps working however `~/.local/bin` changes.

## CI run history

Every check records the CI runs of the PR's current commit (and, after a
push, of the previous one), so each PR keeps the history of all its runs:
a rerun or retrigger is a new run, never an overwrite.

- GitHub Actions: check runs, with GitHub's exact start and end per job.
- Jenkins and other CI posting commit statuses: the status history of each
  context is grouped into runs, from the first status to the first final
  state. A new build URL starts a new run; a build replaced before it
  reported is marked `superseded` and left out of the stats.

## GitHub Enterprise

PRs can live on github.com or any GitHub Enterprise host gh is signed in to:

```bash
gh auth login --hostname github.example.com     # once per host
watchcat pr add https://github.example.com/org/repo/pull/42
watchcat pr add github.example.com/org/repo#42  # same thing
watchcat pr add org/repo#42                     # uses github_host (default github.com)
```

Adding a PR on a host gh isn't signed in to is refused with the
`gh auth login --hostname` command to run. Every gh call (checks, merge,
rerun, logs, review threads) targets the PR's own host. `watchcat health`
and the Health page list each signed-in host and account with its scopes,
the orgs per host, and read access to every watched repo.
`github_host` (Settings page or `config.json`) sets the host for short
`owner/repo#123` refs.

## How it works

Everything runs as background tasks from a SQLite-backed queue (`tasks`
table), executed by a worker pool inside the daemon:

1. A planner enqueues a `pr.check` task for each PR that is due
   (`poll_interval`). `watchcat pr check` enqueues one immediately.
2. `pr.check` reads the PR through `gh api graphql` (CI checks, mergeability,
   review decision, unresolved threads), stores a snapshot and diffs it
   against the previous one.
3. Each change (`ci_failed`, `conflict`, `new_comments`, ...) is written to
   the PR's history and routed to follow-up tasks. Today that is
   `slack.notify`; the review resolver and agent hand-off will plug in as new
   task kinds at the same place (`Router` in `internal/worker`).
4. Failed tasks retry with backoff (30s, 1m, 2m, ...) up to their attempt
   limit; tasks interrupted by a restart are requeued. `watchcat tasks` shows
   them all.

Merged or closed PRs are paused automatically.

## Slack

Create a Slack app with a bot token that has the `chat:write` scope. Store
the token in the OS keychain, not in `config.json`:

```bash
watchcat secret set slack-token    # prompts without echo; or: pbpaste | watchcat secret set slack-token
watchcat service restart
watchcat slack test
```

and set who gets the DMs in `config.json`:

```json
"slack": { "user_id": "U0123456789" }
```

`user_id` is your Slack member ID; the bot DMs you there. Set `channel`
instead (a channel ID the bot was invited to) to post in a channel.
`notify_on` lists which change kinds send a message (default: all except
`pushed`).

## Secrets

Secrets live in the OS secret store, using the same convention as DAP's
`~/.dap/get-secret.sh`: service `watchcat-<name>`, account `$USER`.

| | macOS (login Keychain) | Linux (Secret Service via `secret-tool`) |
|---|---|---|
| read | `security find-generic-password -a $USER -s watchcat-slack-token -w` | `secret-tool lookup service watchcat-slack-token account $USER` |

So `~/.dap/get-secret.sh watchcat-slack-token` returns the token too, and an
entry created with `security add-generic-password` works for watchcat.
`watchcat secret set slack-token` stores it (value from a hidden prompt or
stdin, passed to the store on stdin, never as an argument);
`watchcat secret list` shows each secret's source, masked.

Lookup order: environment variable (`WATCHCAT_SLACK_TOKEN`), then the
keychain, then a plaintext `slack.bot_token` left in an old `config.json`
(works, but `doctor` and `health` warn; `watchcat secret set slack-token`
moves it and deletes it from the file). Only `daemon`, `health` and
`doctor` read secrets, and reading never prompts. On Linux without
`secret-tool` or a running Secret Service, use the environment variable.

## Web UI

`http://127.0.0.1:7878`, a React + TypeScript app (Vite) in `web/ui/`. `make ui`
builds it into `web/dist/app`, which `go:embed` bakes into the binary, so
the installed watchcat is still one file with no Node at runtime. A binary
built without the UI (`make build-go` or plain `go build`) serves a short
page explaining how to build it. Pages refresh every 5s (React Query) and
follow the system light/dark theme:

- **Pull requests**: status, summary and last check per PR, filters for
  "needs attention" and paused, add by ref or URL, check / retrigger / pause.
- **PR detail** (`#/pr/<id>`): current GitHub state, failed checks with links
  to their logs, pending checks, history, and the PR's tasks.
- **PR detail** also has a **workflow rules** panel: each rule's mode,
  whether it matches the PR right now, and when it last fired.
- **Tasks**: every background task with status, attempts and last error or
  result (the exact prompt or command for workflow actions).
- **Health**: one full-width table; a Setup section (binary, symlink, PATH, service, and each secret's
  source shown masked as `xoxb****abcd`) plus the tool checks.
- **CI runs** on the PR page: every recorded run of every job, grouped by
  commit, with result and duration (or "running for ...").
- **Report** (`#/report`): PR lifecycle stats for a period (7/30/90 days or
  all time): PRs watched (open/merged/closed), time to merge (opened ->
  merged on GitHub) p50/p95/max, CI failures per PR p50/p95/max, and a
  per-PR table, plus time to CI result per commit (first job start to last
  job end) and per-job duration p50/p95/max with run and failure counts.
  Same data from `watchcat report [--days N]` and
  `GET /api/v1/report?days=N`.
- **Settings** (`#/settings`): workflow rule modes, merge method, check
  interval, Slack user ID/channel and which changes send a DM. Saved to
  `config.json` and applied immediately, no restart. Secrets are never
  entered or shown in full in the browser.

## Retriggering CI

`watchcat pr retrigger <ref>` (or the Retrigger button) queues a
`pr.retrigger` task that re-runs the failed GitHub Actions jobs with
`gh run rerun --failed`. Checks from other CI systems are reported as skipped.
A follow-up check runs a minute later. The result is in the PR history and
`watchcat tasks`.

## Workflows

After each check, four rules look at the PR's state. Each fires once per
state (per commit, or per new set of failures/threads), not on every check.

| Rule | When | Action |
|---|---|---|
| `merge` | CI green, approved, no unresolved conversations, mergeable, not draft | `pr.merge`: `gh pr merge --<method> --match-head-commit <sha>` |
| `review_threads` | CI green, approved, unresolved conversations | `agent.prompt`: ask the PR's agent session to go through the threads and propose next steps (no pushing) |
| `ci_failed` | CI failed | `agent.prompt`: send failed checks + log tail; flaky → rebase on the base branch, real → prepare a fix |
| `needs_review` | CI green, no approval yet, not draft | Slack "review needed" + `pr.automerge` (`gh pr merge --auto`) |

Each rule is `off`, `dry-run` (default) or `on` in `config.json`:

```json
"workflows": { "merge": "dry-run", "review_threads": "dry-run", "ci_failed": "dry-run", "needs_review": "dry-run" },
"merge_method": "squash"
```

Dry-run does everything except the action: it re-checks GitHub, builds the
prompt or command and stores it as the task's result (visible in
`watchcat tasks`, the PR page and the Tasks page). Actions always re-fetch
the PR right before acting and skip if it changed since the rule fired.

Every rule that fires also sends a Slack DM to `slack.user_id`.

## Agent hand-off (opencode)

Watchcat talks to opencode through its CLI, `opencode api <method> <endpoint>`,
the same way the earlier Python pr-watchdog did. Nothing extra needs to run:
use opencode in your terminal as usual, one process with as many sessions as
you like.

```bash
watchcat agent sessions                         # open sessions with their worktrees
watchcat pr bind dremio/dremio#123 --session ses_abc123   # worktree comes from the session
watchcat pr prompt-ci dremio/dremio#123         # send the CI-failure prompt now (only if the ci_failed rule is "on"; otherwise a dry-run preview)
watchcat pr unbind dremio/dremio#123
```

or pick the session on the PR page. A link is a session plus the worktree it
works in, and is only accepted if opencode reports the session exists, isn't
archived and works in that directory. Before each prompt the session is
checked again, then the prompt is queued
(`post /api/session/<id>/prompt` with `delivery: queue, resume: true`, run
from the worktree). The task records opencode's inbox ID. Prompt tasks are
never retried automatically: if opencode accepted a prompt but the reply was
lost, a retry would make the agent do the work twice.

## MCP server for agents

`watchcat mcp` is an MCP server on stdio, so an agent can link the PR it
just opened to its own session, without you running `pr bind`. Add it to
opencode's config (`~/.config/opencode/opencode.json`, or `opencode.json` in
a repo; check the exact keys against your opencode version):

```json
{
  "mcp": {
    "watchcat": { "type": "local", "command": ["watchcat", "mcp"], "enabled": true }
  }
}
```

and tell agents to use it, e.g. in `AGENTS.md`:

```markdown
After you create a pull request, call the `watchcat.watch_pr` tool so the PR
is linked to this session. Use `watchcat.pr_status` to check CI and reviews.
```

| Tool | What it does |
|---|---|
| `watch_pr(pr?, session_id?)` | watch the PR and link it to the calling session |
| `pr_status(pr?)` | CI result, failing checks with links, review state, unresolved threads, latest CI runs |
| `my_prs(session_id?)` | PRs linked to the session |
| `check_now(pr?)` | re-check now (e.g. after a push) |
| `unwatch_pr(pr?)` | unlink and pause (history kept) |
| `health()` | daemon and tool health |

Defaults: the PR is the one for the git branch checked out where opencode
started the server (`gh pr view`), and the session is the open opencode
session working in that directory (the closest one if several directories
match; with two sessions in the same worktree, the tool asks for
`session_id`). The server describes itself and every tool to the agent
(MCP `instructions` and tool descriptions), so there's no separate help.
There are deliberately no tools to merge, retrigger CI or change settings
or rule modes. The daemon must be running.

## Tool health

`watchcat health` reports whether the tools watchcat relies on are ready,
as the daemon sees them (the service's environment can differ from your
shell). With the daemon down, or with `--local`, it checks from your shell.
`--json` prints the raw report; `GET /api/v1/health` serves the same data
(cached for a minute, `?refresh=1` re-runs it).

| Tool | Required | Checks |
|---|---|---|
| gh | yes | installed, version, every signed-in host and account with scopes (`repo` needed), orgs per host, read access to each watched repo on its host |
| Slack bot | yes | `auth.test`: workspace, bot name/IDs, scopes (`chat:write` needed), channel set |
| Slack CLI | no | installed, version, `slack auth list` |
| opencode | no (needed for agent hand-off) | installed, version |

`watchcat doctor` adds setup checks (config, database, service, daemon).

## Files

Everything lives in `~/.watchcat` (or `$WATCHCAT_HOME`), mode 0700. Secrets
are not stored here; see [Secrets](#secrets).

- `bin/watchcat`: the binary (`~/.local/bin/watchcat` links here)
- `config.json`: `listen`, `poll_interval` (e.g. `"5m"`), `tick_interval`,
  `workers`, `notify_on`, `slack`, `workflows`, `merge_method`, `github_host`
- `watchcat.db`: SQLite (PRs, event history, task queue)
- `logs/watchcat.log`: daemon output when run as a service

Older versions used `~/.watchdog` (before the project was renamed to
watchcat), or before that `~/Library/Application Support/watchdog` (macOS) /
`~/.config/watchdog`; `watchcat init` moves config and database over (stop
the daemon first). `watchcat doctor` checks the binary, symlink and `PATH`.

### Upgrading from watchdog

The rename changes the binary, service, data directory, environment
variables and keychain entries, so an existing watchdog install is not
picked up automatically. Once:

```sh
watchdog service uninstall          # old launchd/systemd service
rm -f ~/.local/bin/watchdog         # old symlink (and ~/.watchdog/bin when done)
make install
watchcat init                       # moves ~/.watchdog/config.json and watchdog.db
watchcat secret set slack-token     # keychain service is now watchcat-slack-token
watchcat service install && watchcat service start
```

Rename `WATCHDOG_HOME` / `WATCHDOG_SLACK_TOKEN` to `WATCHCAT_HOME` /
`WATCHCAT_SLACK_TOKEN` if you set them, and update opencode's MCP config to
run `watchcat mcp`.

## Layout

```
cmd/watchcat/     CLI entry point and subcommands (daemon, pr, service, doctor)
internal/config/  paths and config.json
internal/core/    domain model, PR snapshots/diff, SQLite store + task queue
internal/worker/  worker pool, planner, task handlers (pr.check, slack.notify)
internal/github/  PR state via `gh api graphql`
internal/slack/   bot-token client (chat.postMessage, auth.test)
internal/mcp/     minimal MCP (JSON-RPC over stdio) server; tools live in cmd/watchcat/mcp.go
internal/agent/   opencode client (`opencode api`, run from the session's worktree)
internal/health/  tool checks: gh, Slack bot, Slack CLI, opencode
internal/secrets/ OS keychain access (security / secret-tool, DAP-compatible)
internal/api/     HTTP handlers + the Go client the CLI uses
internal/service/ launchd plist / systemd unit generation and control
web/ui/           React + TypeScript UI source (Vite)
web/dist/app/     built UI, embedded with go:embed (web/embed.go); not committed
```

## API

All under `http://127.0.0.1:7878/api/v1`. Loopback only, no auth; requests
with a non-local `Host` or a cross-site `Origin` are rejected.

| Method | Path | |
|---|---|---|
| GET | `/status` | daemon info |
| GET | `/health?refresh=1` | tool health report |
| GET | `/prs` | list |
| POST | `/prs` | `{"ref": "<PR URL> or owner/repo#123"}`; host must be signed in with gh |
| GET | `/prs/{id}` | PR + history |
| DELETE | `/prs/{id}` | stop watching |
| POST | `/prs/{id}/check` | check now |
| POST | `/prs/{id}/retrigger` | re-run failed CI |
| PUT | `/prs/{id}/agent` | `{"session": "ses_...", "worktree": ""}` link (validated with opencode); empty session unlinks |
| POST | `/prs/{id}/agent/prompt` | `{"prompt": "ci_failed" \| "review_threads"}` send now |
| GET | `/agent/sessions` | open opencode sessions |
| GET | `/prs/{id}/rules` | workflow rules: mode, matches now, last fired |
| GET, PUT | `/settings` | live-editable settings |
| POST | `/prs/{id}/pause`, `/resume` | |
| GET | `/tasks?status=&pr=&limit=` | background tasks |
| GET | `/report?days=N` | PR lifecycle and CI job stats (p50/p95) |
| GET | `/prs/{id}/ci-runs` | the PR's CI run history |
| POST | `/slack/test` | send a test message |

## Development

```bash
make test        # go vet + go test -race (+ UI typecheck if node_modules exists)
make build       # UI + bin/watchcat
make build-go    # binary only, no Node needed
WATCHCAT_HOME=/tmp/wd ./bin/watchcat daemon   # isolated dev instance

cd web/ui && npm ci && npm run dev   # UI with hot reload on :5173, API proxied to :7878
```
