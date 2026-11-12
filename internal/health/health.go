// Package health checks the external tools watchcat depends on (gh, Slack,
// opencode) and reports their state for `watchcat health`, `doctor` and the
// web status page.
package health

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Status of one check or of the whole report.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip" // optional tool that isn't installed
)

// rank orders statuses from best to worst, for picking the overall one.
var rank = map[Status]int{OK: 0, Skip: 0, Warn: 1, Fail: 2}

// Detail is one labelled fact about a tool, optionally with its own status
// (e.g. one repo that gh can't read).
type Detail struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Status Status `json:"status,omitempty"`
}

// Result is the outcome of one tool check.
type Result struct {
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Required bool     `json:"required"`
	Status   Status   `json:"status"`
	Summary  string   `json:"summary"`
	Version  string   `json:"version,omitempty"`
	Details  []Detail `json:"details,omitempty"`
}

func (r *Result) add(label, value string) {
	r.Details = append(r.Details, Detail{Label: label, Value: value})
}
func (r *Result) addS(label, value string, s Status) {
	r.Details = append(r.Details, Detail{label, value, s})
}
func (r *Result) set(s Status, summary string) Result { r.Status, r.Summary = s, summary; return *r }
func (r *Result) worsen(s Status) {
	if rank[s] > rank[r.Status] {
		r.Status = s
	}
}

// Report is the result of running every check.
type Report struct {
	Status    Status    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
	Results   []Result  `json:"results"`
}

// Check inspects one tool.
type Check interface {
	Run(ctx context.Context) Result
}

// Run executes all checks in parallel and combines them. A failing optional
// tool only makes the report a warning.
func Run(ctx context.Context, checks ...Check) Report {
	results := make([]Result, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() { // Go 1.22+ gives each iteration its own i and c
			defer wg.Done()
			results[i] = c.Run(ctx)
		}()
	}
	wg.Wait()

	rep := Report{Status: OK, CheckedAt: time.Now(), Results: results}
	for _, r := range results {
		s := r.Status
		if s == Fail && !r.Required {
			s = Warn
		}
		if rank[s] > rank[rep.Status] {
			rep.Status = s
		}
	}
	return rep
}

// Exec runs external commands. Checks take it as a field so tests can fake
// command output without the real tools installed.
type Exec interface {
	LookPath(name string) (string, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// OSExec is the real implementation.
type OSExec struct{}

func (OSExec) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Output returns stdout. On failure the error carries stderr, which is
// where these tools explain what went wrong.
func (OSExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}
