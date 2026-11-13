# Configuration

Everything lives in `~/.watchcat` (or `$WATCHCAT_HOME`), mode 0700:

- `bin/watchcat`: the binary (`~/.local/bin/watchcat` links here; the service runs this path directly)
- `config.json`: settings, see below
- `watchcat.db`: SQLite (PRs, history, task queue)
- `logs/watchcat.log`: daemon output when run as a service

## config.json

| Key | Meaning |
|---|---|
| `host`, `port` | where the API and web UI are served (default `127.0.0.1`, `7878`); loopback only, restart to apply |
| `poll_interval` | how often each PR is checked, e.g. `"5m"` |
| `tick_interval`, `workers` | planner tick and worker pool size |
| `github_host` | host for short `owner/repo#123` refs (default `github.com`) |
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
`doctor` warns about and `secret set` migrates). `watchcat secret list`
shows where each secret comes from. On Linux without `secret-tool` and a
running keyring, use the environment variable.

## Health checks

`watchcat health` checks the tools as the daemon sees them (`--local` checks
from your shell, `--json` prints the raw report):

| Tool | Required | Checks |
|---|---|---|
| gh | yes | version, signed-in hosts and scopes (`repo`), access to each watched repo |
| Slack bot | yes | `auth.test`, scopes (`chat:write`), channel set |
| Slack CLI | no | version, `slack auth list` |
| opencode | no | version |

`watchcat doctor` adds config, database, service and daemon checks.
