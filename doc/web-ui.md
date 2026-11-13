# Web UI

http://127.0.0.1:7878 (see `host`/`port` in [Configuration](configuration.md)). A React + TypeScript app in `web/ui/`, embedded into
the binary at build time. Pages refresh every 5 seconds.

- **Pull requests**: status per PR, "needs attention" and paused filters, add, check, retrigger, pause.
- **PR detail**: title with watching status and a Watching menu (check now, pause/resume, remove); PR state, link and CI status; current state (review, threads, mergeability, commits, dates); failed and pending checks with log links and Retrigger failed; CI runs by commit. Tabs for History and Tasks (both paged) and Automations (agent session link and workflow rules).
- **Tasks**: background tasks with status, attempts and result (including the exact prompt or command for workflow actions).
- **Health**: the same report as `watchcat health`: setup checks (config, database, binary, symlink, `PATH`, service, daemon, masked secrets) and tool checks.
- **Stats**: for 7/30/90 days or all time, PR counts, time to merge, CI failures per PR, time to CI result and per-job durations (p50/p95/max). Also `watchcat stats [--days N]`.
- **Config**: workflow rule modes, merge method, check interval and Slack options. Saved to `config.json` and applied immediately. Secrets are never shown or entered here.

A binary built without the UI (`make build-go`) serves a page explaining how
to build it.
