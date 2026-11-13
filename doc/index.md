# watchcat

A personal daemon that watches your pull requests (CI, reviews, conflicts),
notifies Slack, and hands work to the opencode session that owns each PR.

`watchcat daemon` runs everything (scheduler, SQLite store, localhost API and
web UI) in one process kept alive by launchd or systemd. The other commands
talk to it.

## Quick start

```bash
make install                 # UI + binary into ~/.watchcat/bin, linked from ~/.local/bin
watchcat init                # writes ~/.watchcat/config.json
watchcat service install     # launchd agent (macOS) or systemd --user unit (Linux)
watchcat service start
watchcat pr add owner/repo#123
```

The web UI is at http://127.0.0.1:7878. Prerequisites and development setup
are in the [README](https://github.com/binarycat0/watchcat#readme).

## Guides

- [Configuration](configuration.md): `config.json`, Slack, secrets, GitHub Enterprise
- [Workflows](workflows.md): automatic merge, CI-failure and review rules, opencode hand-off
- [MCP server](mcp.md): let agents link and check their own PRs
- [Web UI](web-ui.md)
- [HTTP API](api.md)
- [How it works](architecture.md): task queue, CI run history, code layout
- [Upgrading from watchdog](upgrading.md)
