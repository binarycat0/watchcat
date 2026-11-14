# Templates

The Slack messages and AI agent prompts watchcat sends are
[Go templates](https://pkg.go.dev/text/template). Edit them on
**Config › Templates** in the web UI, or as files.

## Where they live

The defaults are built into the binary. Your version of a template is a file
in `~/.watchcat/templates/` (or `$WATCHCAT_HOME/templates/`):

```
~/.watchcat/templates/slack_change.tmpl
~/.watchcat/templates/agent_footer.tmpl
```

- A file is used only if it exists; otherwise the default is.
- **Restore default** deletes the file. You can also just delete it yourself.
- Files are read on every message, so changes apply without a restart.
- Saving text identical to the default deletes the file, so you keep getting
  improved defaults after an upgrade.

The UI checks a template before saving it: it must parse and render with a
sample PR. A file edited by hand that doesn't work is flagged as **broken**
in the UI, and watchcat sends the default instead (with a warning in the log),
so a typo never stops a message.

## The templates

| Name | Sent |
|---|---|
| `slack_change` | Slack message when a watched PR changes (kinds picked on Config › Notifications) |
| `slack_rule` | Slack message when a [workflow rule](workflows.md) fires, in dry-run or on |
| `agent_ci_failed` | prompt for the agent when CI fails (`ci_failed` rule, `watchcat pr prompt-ci`) |
| `agent_review_threads` | prompt for the agent about unresolved review conversations (`review_threads` rule) |
| `agent_header` | shared start of both agent prompts |
| `agent_footer` | shared end of both agent prompts: the ground rules (don't merge, don't force-push, ...) |

The agent prompts include the header and footer with
`{{template "agent_header" .}}` and `{{template "agent_footer" .}}`, so the
ground rules are edited in one place. Leave those lines out to drop them from
one prompt.

## Variables

Every template gets the PR:

| Variable | |
|---|---|
| `.PR.Ref` | `owner/repo#123` |
| `.PR.URL` | link to the PR |
| `.PR.Title` | title |
| `.PR.Repo`, `.PR.Number` | `owner/repo`, `123` |
| `.PR.HeadSHA` | head commit |
| `.PR.Base` | base branch (`main` if unknown) |

Plus, per template:

| Template | Variables |
|---|---|
| `slack_change` | `.Kind` (`ci_failed`, `ci_passed`, `conflict`, `conflict_resolved`, `changes_requested`, `approved`, `new_comments`, `pushed`, `merged`, `closed`), `.Message` (what changed), `.Checks` (newly failed checks) |
| `slack_rule` | `.Rule` (`merge`, `review_threads`, `ci_failed`, `needs_review`), `.Action`, `.DryRun`, `.FailedChecks`, `.Unresolved` |
| `agent_ci_failed` | `.FailedChecks`, `.Logs` (tail of each failed GitHub Actions run, may be empty) |
| `agent_review_threads` | `.Threads`, each with `.N` (1, 2, ...), `.Path`, `.Line`, `.Location` (`path:line`), `.URL` and `.Comments` (`.Author`, `.Body`, `.URL`) |

A check has `.Name` and `.URL` (empty if there's no link).

In the Slack templates, text values (title, message, check names) are already
escaped for Slack, so a PR title can't break the message.

Functions, besides the
[built-in ones](https://pkg.go.dev/text/template#hdr-Functions) (`eq`, `len`, `printf`, ...):

| Function | Example |
|---|---|
| `names` | `{{names .FailedChecks}}` → `test, lint` |
| `indent` | `{{indent "   " .Body}}` indents every line after the first |
| `join`, `lower`, `upper`, `trim` | as in Go's `strings` package |

## Examples

A shorter Slack message without the emoji:

```
{{.PR.Ref}} {{.Message}}: {{.PR.URL}}
```

Ask the agent to answer in another language, for every prompt
(`agent_footer`):

```
Rules: do not merge the PR, do not force-push, and do not resolve review
conversations yourself. Reply in Spanish with a short summary.
```

Different text per change kind (`slack_change`):

```
{{if eq .Kind "merged"}}:rocket: {{.PR.Ref}} is merged!{{else}}{{.PR.Ref}}: {{.Message}}{{end}}
```

Text/template trims whitespace with `{{-` and `-}}`; watchcat also drops
trailing blank lines from the result.
