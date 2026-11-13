# watchcat

A personal daemon that watches your pull requests (CI, reviews, conflicts),
notifies Slack, and hands work to the opencode session that owns each PR.

`watchcat daemon` runs everything (scheduler, SQLite store, localhost API and
web UI) in one process kept alive by launchd or systemd. The other commands
talk to it.

Documentation: https://binarycat0.github.io/watchcat

## Prerequisites

| What | Why | Check / install |
|---|---|---|
| macOS or Linux | runs as a launchd / systemd `--user` service | |
| Go 1.26+ | build | `go version` · `brew install go` |
| Node.js 20+ and npm | build the web UI (not needed at runtime) | `node --version` · `brew install node` |
| `gh` signed in with the `repo` scope | all GitHub access | `gh auth status` · `gh auth login` |
| `~/.local/bin` on `PATH` | `make install` links `watchcat` there | `export PATH="$HOME/.local/bin:$PATH"` |
| Slack bot token with `chat:write` (optional) | DMs about PR changes | [Slack](doc/configuration.md#slack) |
| opencode (optional) | agent hand-off | `opencode --version` |

`watchcat doctor` checks all of this.

## Installation

```bash
make install                 # UI + binary into ~/.watchcat/bin, linked from ~/.local/bin
watchcat init                # writes ~/.watchcat/config.json
watchcat service install     # launchd agent (macOS) or systemd --user unit (Linux)
watchcat service start
```

To upgrade, pull and run `make install` again (it restarts a running
service). `make uninstall` removes the binary and service but keeps
`~/.watchcat`.

## Usage

```bash
watchcat pr add owner/repo#123   # or a PR URL, on github.com or GitHub Enterprise
watchcat pr list
watchcat pr check <id|ref>       # check now
watchcat status
watchcat                         # all commands
```

The web UI is at http://127.0.0.1:7878.

More:

- [Configuration](doc/configuration.md): `config.json`, Slack, secrets, GitHub Enterprise
- [Workflows](doc/workflows.md): automatic merge, CI-failure and review rules, opencode hand-off
- [MCP server](doc/mcp.md): let agents link and check their own PRs
- [Web UI](doc/web-ui.md)
- [HTTP API](doc/api.md)
- [How it works](doc/architecture.md): task queue, CI run history, code layout

## Development

```bash
make test        # go vet, go test -race, UI typecheck
make build       # UI + bin/watchcat
make build-go    # binary only, no Node needed
WATCHCAT_HOME=/tmp/wd ./bin/watchcat daemon   # isolated dev instance
cd web/ui && npm ci && npm run dev            # UI hot reload on :5173, API proxied to :7878
```
