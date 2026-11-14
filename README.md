# watchcat

A personal daemon that watches your pull requests (CI, reviews, conflicts),
notifies Slack, and hands work to the opencode session that owns each PR.

`watchcat daemon` runs everything (scheduler, SQLite store, localhost API and
web UI) in one process kept alive by launchd or systemd. The other commands
talk to it.

Documentation: https://tools.binaryc.at/watchcat

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
- `gh`, signed in with the `repo` scope (`gh auth login`)
- `~/.local/bin` on `PATH`
- optional: a Slack bot token with `chat:write` ([Slack](doc/configuration.md#slack)), `opencode`

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

- [Configuration](doc/configuration.md): `config.json`, Slack, secrets, hosts
- [Workflows](doc/workflows.md): automatic merge, CI-failure and review rules, opencode hand-off
- [MCP server](doc/mcp.md): let agents link and check their own PRs
- [Web UI](doc/web-ui.md)
- [HTTP API](doc/api.md)
- [How it works](doc/architecture.md): task queue, CI run history, code layout

## Development

### Prerequisites

- `go` 1.26+
- `node` 20+ and `npm` (web UI)
- `docker` (docs site)

## Install

```bash
make install     # build UI + binary, install like install.sh does
make test        # go vet, go test -race, UI typecheck
make build       # UI + bin/watchcat
make build-go    # binary only, no Node needed
WATCHCAT_HOME=/tmp/wd ./bin/watchcat daemon   # isolated dev instance
cd web/ui && npm ci && npm run dev            # UI hot reload on :5173, API proxied to :7878 (WATCHCAT_API overrides)
make docs-serve  # docs site on :8000 (Docker)
```
