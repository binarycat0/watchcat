# Web UI

http://127.0.0.1:7878 (see `host`/`port` in [Configuration](configuration.md)). A React + TypeScript app in `web/ui/`, embedded into
the binary at build time. Pages refresh every 5 seconds.

- **Pull requests**: status per PR, "needs attention" and paused filters, add, check, retrigger, pause.
- **PR detail**: GitHub state, failed and pending checks with log links, CI runs by commit, workflow rules, history and tasks.
- **Tasks**: background tasks with status, attempts and result (including the exact prompt or command for workflow actions).
- **Health**: the same report as `watchcat health`: setup checks (config, database, binary, symlink, `PATH`, service, daemon, masked secrets) and tool checks.
- **Report**: for 7/30/90 days or all time, PR counts, time to merge, CI failures per PR, time to CI result and per-job durations (p50/p95/max). Also `watchcat report [--days N]`.
- **Config**: workflow rule modes, merge method, check interval and Slack options. Saved to `config.json` and applied immediately. Secrets are never shown or entered here.

A binary built without the UI (`make build-go`) serves a page explaining how
to build it.
