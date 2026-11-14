# Web UI

Open http://127.0.0.1:7878 (the address is `host`/`port` in
[Configuration](configuration/index.md)). Pages refresh every 5 seconds.

- **Pull requests**: status per PR, "needs attention" and paused filters, add, check, retrigger, pause.
- **PR detail**: title with watching status and a Watching menu (check now, pause/resume, remove); PR state, link and CI status; current state (review, threads, mergeability, commits, dates); failed and pending checks with log links and Retrigger failed; CI runs by commit. Tabs for History and Tasks (both paged) and Automations (agent session link and workflow rules).
- **Tasks**: background tasks with status, attempts and result (including the exact prompt or command for workflow actions).
- **Health**: the same report as `watchcat health`, with the overall status (healthy, needs attention or unhealthy) and a Check button, in tabs: Service (config, database, binary, symlink, `PATH`, service, daemon), and one per provider area: Version control (CLI and its logins), Notifications (bot token, scope and recipient) and AI agent. Each provider tab names its provider, e.g. Version control · GitHub. A tab shows a yellow dot when something in it needs attention and a red dot when it is unhealthy.

- **Config**: tabs for Schedule (check interval), Workflow rules (rule modes), Version control (merge method), Notifications (which PR changes send a notification, recipient, test message, secret status), AI agent (how prompts reach the agent; nothing to set yet) and Templates (edit the notifications and agent prompts with a live preview, restore defaults; see [Templates](configuration/templates.md)). One Save config applies every tab except Templates to `config.json` immediately; each template has its own Save. Secrets are never shown or entered here.
