# watchcat

A personal daemon that watches your pull requests (CI, reviews, conflicts),
notifies you about changes, and hands follow-up work to the AI agent session
that owns each PR.

> **Note:** watchcat currently works only with GitHub (through `gh`), Slack and
> opencode; see [Supported stack](#supported-stack). Support for other
> providers is planned.

`watchcat daemon` runs everything (scheduler, SQLite store, localhost API and
web UI) in one process kept alive by launchd or systemd. The other commands
talk to it.

Documentation: https://tools.binaryc.at/watchcat

## Supported stack

One provider per area for now:

| Area | Provider | Needs |
|---|---|---|
| Version control | GitHub | `gh`, signed in with the `repo` scope (`gh auth login`) |
| Notifications | Slack | a bot token with `chat:write` ([setup](doc/configuration/index.md#notifications-slack)) |
| AI agent (optional) | opencode | `opencode` |

## Installation


```bash
curl -fsSL https://tools.binaryc.at/watchcat/install.sh | sh
```

The script installs the latest release to `~/.watchcat/bin` (no sudo) after checking its SHA-256 checksum. Run it again to upgrade. 

Set `WATCHCAT_VERSION=v0.1.2` to pin a release, or `WATCHCAT_BIN_DIR` to link somewhere other than `~/.local/bin`. 

To uninstall, run `watchcat service uninstall`, then delete `~/.local/bin/watchcat` and `~/.watchcat`.


### Quick start

```bash
watchcat init                # writes ~/.watchcat/config.json
watchcat service install     # launchd agent (macOS) or systemd --user unit (Linux)
watchcat service start
```

### Prerequisites

- macOS or Linux
- `~/.local/bin` on `PATH`
- the provider tools from [Supported stack](#supported-stack)

`watchcat health` checks all of this.

## Usage

```bash
watchcat pr add https://github.com/owner/repo/pull/123
watchcat pr list
watchcat pr check <id|ref>       # check now
watchcat status
watchcat                         # all commands
```

The web UI is at http://127.0.0.1:7878.

More:

- [Configuration](doc/configuration/index.md): `config.json`, providers, secrets, health checks
- [Templates](doc/configuration/templates.md): notification and agent prompt text
- [Workflows](doc/workflows.md): automatic merge, CI-failure and review rules, agent hand-off
- [Web UI](doc/web-ui.md)
- [HTTP API](doc/api.md)
- [MCP server](doc/mcp.md): let agents link and check their own PRs

## Development

### Prerequisites

- `go` 1.26+
- `node` 20+ and `npm` (web UI)
- `docker` or `podman` (docs site)

```bash
make install     # build UI + binary, install like install.sh does
make test        # go vet, go test -race, UI typecheck
```

More in [Contributing](doc/contributing/index.md): dev instance, UI hot
reload, docs site, releasing, and [how it works](doc/contributing/architecture.md).
