# Web UI

http://127.0.0.1:7878 (see `host`/`port` in [Configuration](configuration.md)). A React + TypeScript app in `web/ui/`, embedded into
the binary at build time. Pages refresh every 5 seconds.

- **Pull requests**: status per PR, "needs attention" and paused filters, add, check, retrigger, pause.
- **PR detail**: title with watching status and a Watching menu (check now, pause/resume, remove); PR state, link and CI status; current state (review, threads, mergeability, commits, dates); failed and pending checks with log links and Retrigger failed; CI runs by commit. Tabs for History and Tasks (both paged) and Automations (agent session link and workflow rules).
- **Tasks**: background tasks with status, attempts and result (including the exact prompt or command for workflow actions).
- **Health**: the same report as `watchcat health`, with the overall status (healthy, needs attention or unhealthy) and a Check button, in tabs: Service (config, database, binary, symlink, `PATH`, service, daemon), Version control (gh and its logins), Notifications (Slack bot: token, scope and recipient) and AI agent (opencode). A tab shows a yellow dot when something in it needs attention and a red dot when it is unhealthy.
- **Stats**: for 7/30/90 days or all time, PR counts, time to merge, CI failures per PR, time to CI result and per-job durations (p50/p95/max). Also `watchcat stats [--days N]`.
- **Config**: tabs for Schedule (check interval, default host), Workflow rules (rule modes, merge method), Notifications (which PR changes send a Slack message, Slack target, test message, secret status) and Templates (edit the Slack messages and agent prompts with a live preview, restore defaults; see [Templates](templates.md)). One Save config applies the first three tabs to `config.json` immediately; each template has its own Save. Secrets are never shown or entered here.

A binary built without the UI (`make build-go`) serves a page explaining how
to build it.
