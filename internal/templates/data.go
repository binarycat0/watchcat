package templates

// The data each template gets. Field names are the template variables, so
// renaming one breaks users' templates: treat them as an API.

// PR is shared by every template.
type PR struct {
	Ref     string // owner/repo#123
	URL     string
	Title   string
	Repo    string // owner/repo
	Number  int
	HeadSHA string
	Base    string // base branch, "main" if unknown
}

// Check is a CI check, with a link to its logs if there is one.
type Check struct {
	Name string
	URL  string
}

// ChangeData is the data of slack_change.
type ChangeData struct {
	PR      PR
	Kind    string
	Message string
	Checks  []Check
}

// RuleData is the data of slack_rule.
type RuleData struct {
	PR           PR
	Rule         string
	Action       string
	DryRun       bool
	FailedChecks []Check
	Unresolved   int
}

// CIFailedData is the data of agent_ci_failed.
type CIFailedData struct {
	PR           PR
	FailedChecks []Check
	Logs         string
}

// ReviewThreadsData is the data of agent_review_threads.
type ReviewThreadsData struct {
	PR      PR
	Threads []Thread
}

// Thread is one unresolved review conversation.
type Thread struct {
	N        int    // 1-based position in the list
	Path     string // file
	Line     int    // 0 when the thread isn't on a line
	Location string // path:line, or path
	URL      string // link to the first comment
	Comments []Comment
}

type Comment struct {
	Author string
	Body   string
	URL    string
}

// Sample data for previews and validation.
var samplePR = PR{
	Ref: "acme/api#42", URL: "https://github.com/acme/api/pull/42", Title: "Add rate limiting to the public API",
	Repo: "acme/api", Number: 42, HeadSHA: "3f9c2e1a7b", Base: "main",
}

var sampleChecks = []Check{{Name: "test (ubuntu-latest)", URL: "https://github.com/acme/api/actions/runs/1/job/2"}, {Name: "lint"}}

var (
	SampleChange = ChangeData{PR: samplePR, Kind: "ci_failed", Message: "CI failed: test (ubuntu-latest), lint", Checks: sampleChecks}
	SampleRule   = RuleData{PR: samplePR, Rule: "ci_failed", Action: "ask the agent to investigate the CI failure (flaky: rebase on the base branch; real: prepare a fix)",
		FailedChecks: sampleChecks, Unresolved: 0}
	SampleCIFailed = CIFailedData{PR: samplePR, FailedChecks: sampleChecks,
		Logs: "--- failed jobs of run 1 (last 120 lines) ---\nFAIL: TestLimiter (0.02s)\n    limiter_test.go:31: got 11 requests, want 10\n"}
	SampleReviewThreads = ReviewThreadsData{PR: samplePR, Threads: []Thread{
		{N: 1, Path: "internal/limit/limiter.go", Line: 27, Location: "internal/limit/limiter.go:27", URL: "https://github.com/acme/api/pull/42#discussion_r1",
			Comments: []Comment{
				{Author: "reviewer", Body: "Should this be per user instead of per IP?", URL: "https://github.com/acme/api/pull/42#discussion_r1"},
				{Author: "author", Body: "Good point, but anonymous calls have no user.\nMaybe both?", URL: "https://github.com/acme/api/pull/42#discussion_r2"},
			}},
		{N: 2, Path: "README.md", Location: "README.md", URL: "https://github.com/acme/api/pull/42#discussion_r3",
			Comments: []Comment{{Author: "reviewer", Body: "Please document the limits.", URL: "https://github.com/acme/api/pull/42#discussion_r3"}}},
	}}
)
