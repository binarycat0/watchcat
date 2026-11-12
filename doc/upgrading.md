# Upgrading from watchdog

The rename to watchcat changed the binary, service, data directory,
environment variables and keychain entries, so an existing watchdog install
isn't picked up automatically. Run once:

```sh
watchdog service uninstall          # old launchd/systemd service
rm -f ~/.local/bin/watchdog         # old symlink (remove ~/.watchdog/bin when done)
make install
watchcat init                       # moves ~/.watchdog/config.json and watchdog.db
watchcat secret set slack-token     # keychain service is now watchcat-slack-token
watchcat service install && watchcat service start
```

Also rename `WATCHDOG_HOME` / `WATCHDOG_SLACK_TOKEN` to `WATCHCAT_HOME` /
`WATCHCAT_SLACK_TOKEN` if you set them, and change opencode's MCP config to
run `watchcat mcp`.

`watchcat init` also migrates data from older locations
(`~/Library/Application Support/watchdog` on macOS, `~/.config/watchdog`).
Stop the daemon first.
