package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/binarycat0/watchdog/internal/api"
)

func cmdStatus(e env) error {
	c, cancel := ctx()
	defer cancel()
	st, err := e.client().Status(c)
	if err != nil {
		return err
	}
	fmt.Printf("daemon:  running (pid %d, version %s, up %s)\n", st.PID, st.Version, st.Uptime)
	fmt.Printf("web:     http://%s\n", e.cfg.Listen)
	fmt.Printf("db:      %s\n", st.DBPath)
	fmt.Printf("prs:     %d watched, %d tasks running\n", st.PRs, len(st.Running))
	slackState := "not configured"
	if st.Slack {
		slackState = "configured"
	}
	fmt.Printf("slack:   %s\n", slackState)
	return nil
}

func cmdTasks(e env, args []string) error {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	status := fs.String("status", "", "queued, running, done or failed")
	pr := fs.String("pr", "", "only tasks for this PR (id or ref)")
	limit := fs.Int("limit", 30, "max tasks to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	cl := e.client()
	q := api.TaskQuery{Status: *status, Limit: *limit}
	if *pr != "" {
		id, err := cl.Resolve(c, *pr)
		if err != nil {
			return err
		}
		q.PRID = id
	}
	tasks, err := cl.ListTasks(c, q)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("no tasks")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tPR\tSTATUS\tTRIES\tUPDATED\tERROR")
	for _, t := range tasks {
		pr := "-"
		if t.PRID != nil {
			pr = strconv.FormatInt(*t.PRID, 10)
		}
		updated := t.UpdatedAt
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d/%d\t%s\t%s\n", t.ID, t.Kind, pr, t.Status,
			t.Attempts, t.MaxAttempts, ago(&updated), truncate(t.LastError, 80))
	}
	return w.Flush()
}

func cmdSlack(e env, args []string) error {
	if len(args) != 1 || args[0] != "test" {
		return fmt.Errorf("usage: watchdog slack test")
	}
	c, cancel := ctx()
	defer cancel()
	if err := e.client().SlackTest(c); err != nil {
		return err
	}
	fmt.Println("sent a test message to", e.cfg.Slack.Target())
	return nil
}

// cmdAgent lists the agent's sessions, to find the ID to bind a PR to.
func cmdAgent(e env, args []string) error {
	if len(args) != 1 || args[0] != "sessions" {
		return fmt.Errorf("usage: watchdog agent sessions")
	}
	c, cancel := ctx()
	defer cancel()
	ss, err := e.client().AgentSessions(c)
	if err != nil {
		return err
	}
	if len(ss) == 0 {
		fmt.Println("no open opencode sessions")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tDIRECTORY\tTITLE")
	for _, s := range ss {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.ID, s.Directory, truncate(s.Title, 60))
	}
	return w.Flush()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func cmdPR(e env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: watchdog pr add|list|show|remove|check|retrigger|bind|unbind|pause|resume")
	}
	c, cancel := ctx()
	defer cancel()
	cl := e.client()
	sub, rest := args[0], args[1:]

	if sub == "list" || sub == "ls" {
		prs, err := cl.ListPRs(c)
		if err != nil {
			return err
		}
		if len(prs) == 0 {
			fmt.Println("no PRs watched; add one with `watchdog pr add owner/repo#123`")
			return nil
		}
		// tabwriter aligns columns, like str.ljust across rows.
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tPR\tSTATUS\tLAST CHECKED\tSUMMARY")
		for _, p := range prs {
			status := p.Status
			if p.Paused {
				status = "paused"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", p.ID, p.Ref(), status, ago(p.LastChecked), p.Summary)
		}
		return w.Flush()
	}

	if sub == "bind" || sub == "unbind" {
		return cmdBind(c, cl, sub, rest)
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: watchdog pr %s <id|owner/repo#123|url>", sub)
	}
	arg := rest[0]

	if sub == "add" {
		p, err := cl.AddPR(c, arg)
		if err != nil {
			return err
		}
		fmt.Printf("watching %s (id %d)\n", p.Ref(), p.ID)
		return nil
	}

	id, err := cl.Resolve(c, arg)
	if err != nil {
		return err
	}
	switch sub {
	case "show":
		d, err := cl.GetPR(c, id)
		if err != nil {
			return err
		}
		fmt.Printf("%s  %s\n", d.Ref(), d.URL)
		if d.AgentSession != "" {
			fmt.Printf("agent:   %s in %s\n", d.AgentSession, d.AgentWorktree)
		}
		if d.Snapshot != nil {
			fmt.Printf("title:   %s\n", d.Snapshot.Title)
			for _, ch := range d.Snapshot.FailedChecks {
				fmt.Printf("failed:  %s %s\n", ch.Name, ch.URL)
			}
		}
		fmt.Printf("status:  %s %s\n", d.Status, d.Summary)
		fmt.Printf("paused:  %v\n", d.Paused)
		fmt.Printf("checked: %s, next in %s\n", ago(d.LastChecked), time.Until(d.NextCheck).Round(time.Second))
		fmt.Println("history:")
		for _, ev := range d.Events {
			fmt.Printf("  %s  %-8s %s\n", ev.CreatedAt.Format("2006-01-02 15:04:05"), ev.Kind, ev.Message)
		}
		return nil
	case "remove", "rm":
		if err := cl.RemovePR(c, id); err != nil {
			return err
		}
		fmt.Println("removed", arg)
		return nil
	case "check":
		if err := cl.CheckNow(c, id); err != nil {
			return err
		}
		fmt.Println("check queued for", arg)
		return nil
	case "prompt-ci", "prompt-review":
		kind := map[string]string{"prompt-ci": "ci_failed", "prompt-review": "review_threads"}[sub]
		dry, err := cl.PromptAgent(c, id, kind)
		if err != nil {
			return err
		}
		if dry {
			fmt.Printf("dry-run: the %s rule isn't on, so the prompt is built but not sent (see `watchdog tasks --pr %s`)\n", kind, arg)
		} else {
			fmt.Println("prompt queued for", arg, "(see `watchdog tasks --pr", arg+"`)")
		}
		return nil
	case "retrigger":
		if err := cl.Retrigger(c, id); err != nil {
			return err
		}
		fmt.Println("retrigger queued for", arg, "(see `watchdog tasks --pr", arg+"`)")
		return nil
	case "pause", "resume":
		if err := cl.SetPaused(c, id, sub == "pause"); err != nil {
			return err
		}
		fmt.Printf("%sd %s\n", sub, arg)
		return nil
	}
	return fmt.Errorf("unknown pr command %q", sub)
}

// cmdBind links (or unlinks) a PR to the agent session that owns it:
//
//	watchdog pr bind <ref> --session <id>
//	watchdog pr unbind <ref>
func cmdBind(c context.Context, cl *api.Client, sub string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: watchdog pr %s <id|ref> [--session ses_... [--worktree /path]]", sub)
	}
	ref := args[0]
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	session := fs.String("session", "", "opencode session ID (see `watchdog agent sessions`)")
	worktree := fs.String("worktree", "", "the session's worktree; default: the directory the session works in")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if sub == "bind" && *session == "" {
		return fmt.Errorf("--session is required")
	}
	if sub == "unbind" {
		*session = ""
	}
	id, err := cl.Resolve(c, ref)
	if err != nil {
		return err
	}
	if err := cl.BindAgent(c, id, *session, *worktree); err != nil {
		return err
	}
	if *session == "" {
		fmt.Println("unlinked", ref)
	} else {
		d, err := cl.GetPR(c, id)
		if err != nil {
			return err
		}
		fmt.Printf("linked %s to session %s in %s\n", ref, *session, d.AgentWorktree)
	}
	return nil
}

// ago formats a nullable time as "3m ago" or "never".
func ago(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return time.Since(*t).Round(time.Second).String() + " ago"
}

// cmdReport prints PR lifecycle stats.
func cmdReport(e env, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	days := fs.Int("days", 0, "only PRs open at some point in the last N days (0 = all time)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	r, err := e.client().Report(c, *days)
	if err != nil {
		return err
	}
	period := "all time"
	if *days > 0 {
		period = fmt.Sprintf("last %d days", *days)
	}
	fmt.Printf("PRs (%s): %d watched, %d open, %d merged, %d closed, %d paused\n",
		period, r.Watched, r.Open, r.Merged, r.Closed, r.Paused)
	if r.TimeToMerge.Count > 0 {
		fmt.Printf("time to merge (%d PRs): p50 %s, p95 %s, max %s\n", r.TimeToMerge.Count,
			human(r.TimeToMerge.P50), human(r.TimeToMerge.P95), human(r.TimeToMerge.Max))
	} else {
		fmt.Println("time to merge: no merged PRs yet")
	}
	if r.MissingTimes > 0 {
		fmt.Printf("  (%d merged PRs still missing GitHub timestamps; they fill in on the next check)\n", r.MissingTimes)
	}
	fmt.Printf("CI failures per PR (%d PRs): p50 %g, p95 %g, max %g\n",
		r.CIFailures.Count, r.CIFailures.P50, r.CIFailures.P95, r.CIFailures.Max)
	if r.PipelineTime.Count > 0 {
		fmt.Printf("time to CI result per commit (%d commits): p50 %s, p95 %s, max %s\n", r.PipelineTime.Count,
			human(r.PipelineTime.P50), human(r.PipelineTime.P95), human(r.PipelineTime.Max))
	}
	if len(r.Jobs) == 0 {
		fmt.Println("CI jobs: no runs recorded yet")
		return nil
	}
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tRUNS\tFAILED\tRUNNING\tP50\tP95\tMAX")
	for _, j := range r.Jobs {
		d := j.Duration
		p50, p95, mx := "-", "-", "-"
		if d.Count > 0 {
			p50, p95, mx = human(d.P50), human(d.P95), human(d.Max)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t%s\t%s\n", j.Name, j.Runs, j.Failures, j.Running, p50, p95, mx)
	}
	return w.Flush()
}

// human formats seconds as "3d 4h", "5h 12m" or "18m".
func human(secs float64) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
