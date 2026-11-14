# Workflows

After each check, four rules look at the PR. Each fires once per state
(per commit, or per new set of failures or threads), not on every check.

| Rule | When | Action |
|---|---|---|
| `merge` | CI green, approved, no unresolved threads, mergeable, not draft | merge with the configured method, only if the head commit hasn't moved |
| `review_threads` | CI green, approved, unresolved threads | ask the PR's agent session to go through the threads (no pushing) |
| `ci_failed` | CI failed | send failed checks and log tail to the agent: rebase if flaky, fix if real |
| `needs_review` | CI green, no approval, not draft | "review needed" notification and auto-merge |

Each rule is `off`, `dry-run` (default) or `on`:

```json
"workflows": { "merge": "dry-run", "review_threads": "dry-run", "ci_failed": "dry-run", "needs_review": "dry-run" },
"github": { "merge_method": "squash" }
```

Dry-run builds the prompt or command and stores it as the task result
without running it. Actions re-fetch the PR first and skip if it changed.
Every rule that fires also sends a notification. The notification and the
agent prompts can be changed; see [Templates](configuration/templates.md).

## Retriggering CI

`watchcat pr retrigger <ref>` (or the Retrigger button) re-runs failed
GitHub Actions jobs and checks again a minute later. Other CI systems are skipped.

## Agent hand-off

Link a PR to the AI agent session working on it:

```bash
watchcat agent sessions                                   # open sessions and their worktrees
watchcat pr bind https://github.com/owner/repo/pull/123 --session ses_abc123
watchcat pr prompt-ci https://github.com/owner/repo/pull/123   # send the CI-failure prompt now
watchcat pr unbind https://github.com/owner/repo/pull/123
```

or pick the session on the PR page, or let the agent do it through the
[MCP server](mcp.md). A link is only accepted if the agent reports the
session exists and works in that worktree. How watchcat reaches the agent is
in [Configuration](configuration/index.md#ai-agent-opencode). Prompts are queued into the
session and never retried automatically, so the agent never gets the same
work twice.
