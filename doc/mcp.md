# MCP server

`watchcat mcp` is an MCP server on stdio. It lets an agent link the PR it
just opened to its own session and check on it. The daemon must be running.

Add it to opencode's config (`~/.config/opencode/opencode.json` or a repo's
`opencode.json`):

```json
{
  "mcp": {
    "watchcat": { "type": "local", "command": ["watchcat", "mcp"], "enabled": true }
  }
}
```

and tell agents to use it, e.g. in `AGENTS.md`:

```markdown
After you create a pull request, call the `watchcat.watch_pr` tool so the PR
is linked to this session. Use `watchcat.pr_status` to check CI and reviews.
```

| Tool | What it does |
|---|---|
| `watch_pr(pr?, session_id?)` | watch the PR and link it to the calling session |
| `pr_status(pr?)` | CI result, failing checks, review state, unresolved threads, recent CI runs |
| `my_prs(session_id?)` | PRs linked to the session |
| `check_now(pr?)` | re-check now, e.g. after a push |
| `unwatch_pr(pr?)` | unlink and pause (history kept) |
| `health()` | daemon and tool health |

By default the PR is the one for the branch checked out where opencode
started, and the session is the one working in that directory. There are
deliberately no tools to merge, retrigger CI or change settings.
