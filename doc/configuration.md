# Configuration

Everything lives in `~/.watchcat` (or `$WATCHCAT_HOME`), mode 0700:

- `bin/watchcat`: the binary (`~/.local/bin/watchcat` links here; the service runs this path directly)
- `config.json`: settings, see below
- `watchcat.db`: SQLite (PRs, history, task queue)
- `logs/watchcat.log`: daemon output when run as a service
- `templates/`: your versions of the Slack message and agent prompt templates, see [Templates](templates.md)

## config.json

| Key | Meaning |
|---|---|
| `host`, `port` | where the API and web UI are served (default `127.0.0.1`, `7878`); loopback only, restart to apply |
| `poll_interval` | how often each PR is checked, e.g. `"5m"` |
| `tick_interval`, `workers` | planner tick and worker pool size |
| `slack` | `user_id` or `channel`, see [Slack](#slack) |
| `notify_on` | change kinds that send a message (default: all except `pushed`) |
| `workflows`, `merge_method` | see [Workflows](workflows.md) |

Most of these can also be changed on the Settings page, which applies them
without a restart.

## GitHub Enterprise

PRs can be on any host `gh` is signed in to:

```bash
gh auth login --hostname github.example.com
watchcat pr add https://github.example.com/org/repo/pull/42   # or github.example.com/org/repo#42
```

Adding a PR on a host `gh` isn't signed in to is refused. `watchcat health`
lists each signed-in host, its scopes and access to watched repos.

## Slack

Create a Slack app with a bot token that has `chat:write`, then:

```bash
watchcat secret set slack-token    # hidden prompt, or: pbpaste | watchcat secret set slack-token
watchcat service restart
watchcat slack test
```

Set the recipient in `config.json`, either your member ID or a channel the
bot was invited to:

```json
"slack": { "user_id": "U0123456789" }
```

## Secrets

Secrets are kept in the OS secret store under service `watchcat-<name>`,
account `$USER` (the same convention as DAP's `~/.dap/get-secret.sh`):

- macOS: `security find-generic-password -a $USER -s watchcat-slack-token -w`
- Linux: `secret-tool lookup service watchcat-slack-token account $USER`

Lookup order is the `WATCHCAT_SLACK_TOKEN` environment variable, then the
keychain, then a plaintext `slack.bot_token` in an old `config.json` (which
`watchcat health` warns about and `secret set` migrates). `watchcat secret list`
shows where each secret comes from. On Linux without `secret-tool` and a
running keyring, use the environment variable.

## Health checks

`watchcat health` runs one list of checks, shared with the API
(`/api/v1/health`), the MCP `health` tool and the web UI's Health page. It asks
the daemon, so you see what the service sees; if the daemon isn't running it
runs the checks from your shell instead. `--local` always checks from your
shell (handy when the service's environment differs, e.g. `PATH`), `--refresh`
skips the daemon's one-minute cache, and `--json` prints the raw report. It
exits non-zero when a required check fails.

| Check | Required | Checks |
|---|---|---|
| Setup | yes | config file, database, binary and symlink, `PATH`, service, daemon, where each secret comes from (masked) |
| gh | yes | version, signed-in hosts and scopes (`repo`), access to each watched repo |
| Slack bot | yes | bot token (set, accepted by `auth.test`, `chat:write` scope) and recipient (`slack.user_id` or `slack.channel`), each listed whether set or not |
| opencode | no | version |
