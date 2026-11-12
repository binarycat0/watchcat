// Package github reads PR state through the gh CLI, so watchdog reuses your
// existing `gh auth login` instead of managing its own token.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/binarycat0/watchdog/internal/core"
)

// Fetcher returns the current snapshot of a PR. The worker depends on this
// interface, so tests can substitute a fake.
type Fetcher interface {
	Fetch(ctx context.Context, ref core.PRRef) (core.Snapshot, error)
}

// GH shells out to `gh api graphql`.
type GH struct {
	Bin string // path to gh; "" = look up on PATH
}

const query = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      title state isDraft mergeable reviewDecision headRefOid baseRefName autoMergeRequest{enabledAt} createdAt mergedAt closedAt
      commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){nodes{
        __typename
        ... on CheckRun{name status conclusion detailsUrl}
        ... on StatusContext{context state targetUrl}
      }}}}}}
      reviewThreads(first:100){nodes{isResolved isOutdated}}
    }
  }
}`

// response mirrors just the parts of the GraphQL result we read. Anonymous
// nested structs save declaring a named type for every level.
type response struct {
	Data struct {
		Repository struct {
			PullRequest *struct {
				Title          string     `json:"title"`
				State          string     `json:"state"`
				IsDraft        bool       `json:"isDraft"`
				Mergeable      string     `json:"mergeable"`
				ReviewDecision string     `json:"reviewDecision"`
				HeadRefOid     string     `json:"headRefOid"`
				BaseRefName    string     `json:"baseRefName"`
				CreatedAt      *time.Time `json:"createdAt"`
				MergedAt       *time.Time `json:"mergedAt"`
				ClosedAt       *time.Time `json:"closedAt"`
				AutoMerge      *struct {
					EnabledAt string `json:"enabledAt"`
				} `json:"autoMergeRequest"`
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								Contexts struct {
									Nodes []checkNode `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
				ReviewThreads struct {
					Nodes []struct {
						IsResolved bool `json:"isResolved"`
						IsOutdated bool `json:"isOutdated"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// checkNode is either a GitHub Actions CheckRun or a commit StatusContext
// (what Jenkins and most external CI post).
type checkNode struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsUrl"`
	Context    string `json:"context"`
	State      string `json:"state"`
	TargetURL  string `json:"targetUrl"`
}

func (g GH) Fetch(ctx context.Context, ref core.PRRef) (core.Snapshot, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, graphqlArgs(ref, query)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	// gh exits non-zero on GraphQL errors but still prints the JSON body,
	// which carries the useful message, so try to parse before giving up.
	snap, perr := Parse(out)
	if perr == nil {
		return snap, nil
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = perr.Error()
		}
		return core.Snapshot{}, fmt.Errorf("gh api graphql: %s", msg)
	}
	return core.Snapshot{}, perr
}

// Parse turns the GraphQL JSON into a Snapshot. Split out so tests can feed
// recorded responses.
func Parse(body []byte) (core.Snapshot, error) {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return core.Snapshot{}, fmt.Errorf("decode GitHub response: %w", err)
	}
	if len(r.Errors) > 0 {
		return core.Snapshot{}, fmt.Errorf("GitHub: %s", r.Errors[0].Message)
	}
	pr := r.Data.Repository.PullRequest
	if pr == nil {
		return core.Snapshot{}, fmt.Errorf("GitHub: pull request not found")
	}
	s := core.Snapshot{
		Title: pr.Title, State: pr.State, Draft: pr.IsDraft, Mergeable: pr.Mergeable,
		ReviewDecision: pr.ReviewDecision, HeadSHA: pr.HeadRefOid, CI: core.CINone,
		BaseRef: pr.BaseRefName, AutoMerge: pr.AutoMerge != nil,
		OpenedAt: pr.CreatedAt, MergedAt: pr.MergedAt, ClosedAt: pr.ClosedAt,
	}
	for _, t := range pr.ReviewThreads.Nodes {
		if !t.IsResolved && !t.IsOutdated {
			s.Unresolved++
		}
	}
	if len(pr.Commits.Nodes) == 0 || pr.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return s, nil
	}
	nodes := pr.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts.Nodes
	passed := 0
	for _, n := range nodes {
		name, url, state := classify(n)
		switch state {
		case core.CIFailure:
			s.FailedChecks = append(s.FailedChecks, core.Check{Name: name, URL: url})
		case core.CIPending:
			s.PendingChecks = append(s.PendingChecks, name)
		case core.CISuccess:
			passed++
		}
	}
	switch {
	case len(s.FailedChecks) > 0:
		s.CI = core.CIFailure
	case len(s.PendingChecks) > 0:
		s.CI = core.CIPending
	case passed > 0:
		s.CI = core.CISuccess
	}
	return s, nil
}

// classify maps both node types onto success/failure/pending. Skipped and
// neutral checks return "" and are ignored.
func classify(n checkNode) (name, url, state string) {
	if n.Typename == "StatusContext" {
		switch n.State {
		case "SUCCESS":
			state = core.CISuccess
		case "FAILURE", "ERROR":
			state = core.CIFailure
		case "PENDING", "EXPECTED":
			state = core.CIPending
		}
		return n.Context, n.TargetURL, state
	}
	if n.Status != "COMPLETED" {
		return n.Name, n.DetailsURL, core.CIPending
	}
	switch n.Conclusion {
	case "SUCCESS":
		state = core.CISuccess
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
		state = core.CIFailure
	}
	return n.Name, n.DetailsURL, state
}

// actionsRunRe extracts the run ID from a GitHub Actions check URL such as
// https://github.com/o/r/actions/runs/123/job/456.
var actionsRunRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// ActionsRunID returns the Actions run ID behind a check URL, or "" for
// checks from external CI (Jenkins etc.).
func ActionsRunID(checkURL string) string {
	if m := actionsRunRe.FindStringSubmatch(checkURL); m != nil {
		return m[1]
	}
	return ""
}

// RerunFailed re-runs only the failed jobs of an Actions run.
func (g GH) RerunFailed(ctx context.Context, ref core.PRRef, runID string) error {
	return g.run(ctx, "run", "rerun", runID, "--failed", "-R", ref.Slug())
}

// graphqlArgs builds `gh api graphql` for a PR query on the PR's host.
func graphqlArgs(ref core.PRRef, q string) []string {
	return []string{"api", "graphql", "--hostname", ref.Host,
		"-F", "owner=" + ref.Owner, "-F", "repo=" + ref.Repo, "-F", "number=" + strconv.Itoa(ref.Number),
		"-f", "query=" + q}
}

func (g GH) run(ctx context.Context, args ...string) error {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh %s: %s", args[0]+" "+args[1], strings.TrimSpace(string(out)))
	}
	return nil
}

// Merge merges a PR, but only if its head is still expectSHA, so a commit
// pushed after watchdog decided to merge is never merged unseen.
func (g GH) Merge(ctx context.Context, ref core.PRRef, method, expectSHA string) error {
	return g.run(ctx, "pr", "merge", strconv.Itoa(ref.Number), "-R", ref.Slug(),
		"--"+method, "--match-head-commit", expectSHA)
}

// EnableAutoMerge turns on GitHub auto-merge for a PR.
func (g GH) EnableAutoMerge(ctx context.Context, ref core.PRRef, method string) error {
	return g.run(ctx, "pr", "merge", strconv.Itoa(ref.Number), "-R", ref.Slug(), "--auto", "--"+method)
}

// FailedLog returns the last maxLines lines of the failed jobs' logs of an
// Actions run.
func (g GH) FailedLog(ctx context.Context, ref core.PRRef, runID string, maxLines int) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	out, err := exec.CommandContext(ctx, bin, "run", "view", runID, "-R", ref.Slug(), "--log-failed").Output()
	if err != nil {
		return "", fmt.Errorf("gh run view --log-failed: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}

// Thread is an unresolved review conversation.
type Thread struct {
	Path     string    `json:"path"`
	Line     int       `json:"line"`
	Comments []Comment `json:"comments"`
}

type Comment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

const threadsQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){pullRequest(number:$number){
    reviewThreads(first:100){nodes{isResolved isOutdated path line
      comments(first:20){nodes{author{login} body url}}}}}}}`

// UnresolvedThreads returns the PR's open, non-outdated review threads.
func (g GH) UnresolvedThreads(ctx context.Context, ref core.PRRef) ([]Thread, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	out, err := exec.CommandContext(ctx, bin, graphqlArgs(ref, threadsQuery)...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh api graphql (threads): %w", err)
	}
	return parseThreads(out)
}

func parseThreads(body []byte) ([]Thread, error) {
	var r struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool   `json:"isResolved"`
							IsOutdated bool   `json:"isOutdated"`
							Path       string `json:"path"`
							Line       int    `json:"line"`
							Comments   struct {
								Nodes []struct {
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
									Body string `json:"body"`
									URL  string `json:"url"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	var out []Thread
	for _, n := range r.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if n.IsResolved || n.IsOutdated {
			continue
		}
		t := Thread{Path: n.Path, Line: n.Line}
		for _, c := range n.Comments.Nodes {
			t.Comments = append(t.Comments, Comment{Author: c.Author.Login, Body: c.Body, URL: c.URL})
		}
		out = append(out, t)
	}
	return out, nil
}

// Account is one gh login on one host, from `gh auth status --json hosts`.
type Account struct {
	Host        string `json:"host"`
	Login       string `json:"login"`
	Active      bool   `json:"active"`
	OK          bool   `json:"ok"` // signed in and the token works
	Error       string `json:"error,omitempty"`
	Scopes      string `json:"scopes"`
	TokenSource string `json:"token_source"`
}

// ParseAuthStatus reads `gh auth status --json hosts` output.
func ParseAuthStatus(body []byte) ([]Account, error) {
	var r struct {
		Hosts map[string][]struct {
			State       string `json:"state"`
			Error       string `json:"error"`
			Active      bool   `json:"active"`
			Login       string `json:"login"`
			TokenSource string `json:"tokenSource"`
			Scopes      string `json:"scopes"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse gh auth status: %w", err)
	}
	var out []Account
	for host, accounts := range r.Hosts {
		for _, a := range accounts {
			out = append(out, Account{Host: host, Login: a.Login, Active: a.Active, OK: a.State == "success",
				Error: a.Error, Scopes: a.Scopes, TokenSource: a.TokenSource})
		}
	}
	// Map iteration order is random in Go; sort for stable output.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Login < out[j].Login
	})
	return out, nil
}

// Accounts lists the hosts and logins gh is signed in to.
func (g GH) Accounts(ctx context.Context) ([]Account, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	// gh exits non-zero if any account has a problem but still prints JSON.
	out, err := exec.CommandContext(ctx, bin, "auth", "status", "--json", "hosts").Output()
	accounts, perr := ParseAuthStatus(out)
	if perr != nil {
		if err != nil {
			return nil, fmt.Errorf("gh auth status: %w", err)
		}
		return nil, perr
	}
	return accounts, nil
}

// SignedInHosts returns the hosts with at least one working account.
func SignedInHosts(accounts []Account) []string {
	var hosts []string
	for _, a := range accounts {
		if a.OK && !slices.Contains(hosts, a.Host) {
			hosts = append(hosts, a.Host)
		}
	}
	return hosts
}
