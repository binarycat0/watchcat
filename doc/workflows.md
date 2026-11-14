# Workflows

After each check, four rules look at the PR. Each fires once per state
(per commit, or per new set of failures or threads), not on every check.

| Rule | When | Action |
|---|---|---|
| `merge` | CI green, approved, no unresolved threads, mergeable, not draft | `gh pr merge --<method> --match-head-commit <sha>` |
| `review_threads` | CI green, approved, unresolved threads | ask the PR's agent session to go through the threads (no pushing) |
| `ci_failed` | CI failed | send failed checks and log tail to the agent: rebase if flaky, fix if real |
| `needs_review` | CI green, no approval, not draft | Slack "review needed" and `gh pr merge --auto` |

Each rule is `off`, `dry-run` (default) or `on`:

```json
"workflows": { "merge": "dry-run", "review_threads": "dry-run", "ci_failed": "dry-run", "needs_review": "dry-run" },
"merge_method": "squash"
```

Dry-run builds the prompt or command and stores it as the task result
without running it. Actions re-fetch the PR first and skip if it changed.
Every rule that fires also sends a Slack message. The message and the agent
prompts can be changed; see [Templates](templates.md).

## Retriggering CI

`watchcat pr retrigger <ref>` (or the Retrigger button) re-runs failed
GitHub Actions jobs with `gh run rerun --failed` and checks again a minute
later. Other CI systems are skipped.

## Agent hand-off (opencode)

watchcat talks to opencode through `opencode api`, so nothing extra needs to
run. Link a PR to the session working on it:

```bash
watchcat agent sessions                                   # open sessions and their worktrees
watchcat pr bind https://github.com/owner/repo/pull/123 --session ses_abc123
watchcat pr prompt-ci https://github.com/owner/repo/pull/123   # send the CI-failure prompt now
watchcat pr unbind https://github.com/owner/repo/pull/123
```

or pick the session on the PR page, or let the agent do it through the
[MCP server](mcp.md). A link is only accepted if opencode reports the
session exists and works in that worktree. Prompts are queued into the
session and never retried automatically, so the agent never gets the same
work twice.
