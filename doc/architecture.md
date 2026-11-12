# How it works

One process, `watchcat daemon`, runs everything. Work happens as tasks in a
SQLite-backed queue, run by a worker pool:

1. A planner queues a `pr.check` for each PR that is due (`poll_interval`).
2. `pr.check` reads the PR with `gh api graphql` (checks, mergeability,
   reviews, unresolved threads), stores a snapshot and diffs it with the last one.
3. Each change (`ci_failed`, `conflict`, `new_comments`, ...) goes into the
   PR's history and is routed to follow-up tasks such as `slack.notify` and
   the [workflow rules](workflows.md) (`Router` in `internal/worker`).
4. Failed tasks retry with backoff; tasks interrupted by a restart are
   requeued. `watchcat tasks` lists them.

Merged or closed PRs are paused automatically.

## CI run history

Each check records the CI runs of the PR's current commit (and the previous
one after a push), so reruns add runs instead of overwriting them.

- GitHub Actions: check runs with exact per-job start and end times.
- Jenkins and other commit-status CI: each context's statuses are grouped
  into runs. A new build URL starts a new run; a build replaced before it
  reported is marked `superseded` and left out of stats.

## Code layout

```
cmd/watchcat/     CLI entry point and subcommands
internal/config/  paths and config.json
internal/core/    domain model, snapshots and diff, SQLite store and task queue
internal/worker/  worker pool, planner, task handlers
internal/github/  PR state via gh
internal/slack/   bot-token client
internal/mcp/     MCP server (tools in cmd/watchcat/mcp.go)
internal/agent/   opencode client
internal/health/  tool checks
internal/secrets/ OS keychain access
internal/api/     HTTP handlers and the client the CLI uses
internal/service/ launchd / systemd unit generation
web/ui/           React UI source; built into web/dist/app and embedded
```
