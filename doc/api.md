# HTTP API

Served under `http://127.0.0.1:7878/api/v1`. Loopback only, no auth;
requests with a non-local `Host` or a cross-site `Origin` are rejected.

| Method | Path | |
|---|---|---|
| GET | `/status` | daemon info |
| GET | `/health?refresh=1` | tool health (cached for a minute) |
| GET | `/prs` | list |
| POST | `/prs` | `{"ref": "<PR URL> or owner/repo#123"}` |
| GET | `/prs/{id}` | PR and history |
| DELETE | `/prs/{id}` | stop watching |
| POST | `/prs/{id}/check` | check now |
| POST | `/prs/{id}/retrigger` | re-run failed CI |
| POST | `/prs/{id}/pause`, `/prs/{id}/resume` | |
| PUT | `/prs/{id}/agent` | `{"session": "ses_...", "worktree": ""}`; empty session unlinks |
| POST | `/prs/{id}/agent/prompt` | `{"prompt": "ci_failed" \| "review_threads"}` |
| GET | `/prs/{id}/rules` | workflow rules: mode, matches now, last fired |
| GET | `/prs/{id}/ci-runs` | CI run history |
| GET | `/agent/sessions` | open opencode sessions |
| GET | `/tasks?status=&pr=&limit=` | background tasks |
| GET | `/report?days=N` | PR lifecycle and CI stats |
| GET, PUT | `/settings` | live-editable settings |
| POST | `/slack/test` | send a test message |
