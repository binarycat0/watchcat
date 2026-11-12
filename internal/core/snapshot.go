package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Snapshot is what we know about a PR on GitHub at one point in time. It is
// stored as JSON on the PR row so the next check can diff against it.
type Snapshot struct {
	Title          string   `json:"title"`
	State          string   `json:"state"`           // OPEN, CLOSED, MERGED
	Draft          bool     `json:"draft"`           //
	Mergeable      string   `json:"mergeable"`       // MERGEABLE, CONFLICTING, UNKNOWN
	ReviewDecision string   `json:"review_decision"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	HeadSHA        string   `json:"head_sha"`
	CI             string   `json:"ci"` // CIs below
	FailedChecks   []Check  `json:"failed_checks,omitempty"`
	PendingChecks  []string `json:"pending_checks,omitempty"`
	Unresolved     int      `json:"unresolved_threads"`
	BaseRef        string   `json:"base_ref,omitempty"`
	AutoMerge      bool     `json:"auto_merge,omitempty"` // GitHub auto-merge is enabled
	// Lifecycle timestamps from GitHub, for the report. Pointers: nil means
	// not happened (or not fetched yet, for snapshots from older versions).
	OpenedAt *time.Time `json:"opened_at,omitempty"`
	MergedAt *time.Time `json:"merged_at,omitempty"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
}

// Check is one failing CI check, with a link to its logs.
type Check struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Overall CI states.
const (
	CINone    = "none"
	CIPending = "pending"
	CISuccess = "success"
	CIFailure = "failure"
)

// PR statuses, most urgent first. Status() picks the first that applies.
const (
	StatusMerged           = "merged"
	StatusClosed           = "closed"
	StatusConflict         = "conflict"
	StatusCIFailed         = "ci_failed"
	StatusChangesRequested = "changes_requested"
	StatusUnresolved       = "unresolved_comments"
	StatusCIRunning        = "ci_running"
)

// Status reduces a snapshot to the single word shown in lists.
func (s Snapshot) Status() string {
	switch {
	case s.State == "MERGED":
		return StatusMerged
	case s.State == "CLOSED":
		return StatusClosed
	case s.Mergeable == "CONFLICTING":
		return StatusConflict
	case s.CI == CIFailure:
		return StatusCIFailed
	case s.ReviewDecision == "CHANGES_REQUESTED":
		return StatusChangesRequested
	case s.Unresolved > 0:
		return StatusUnresolved
	case s.CI == CIPending:
		return StatusCIRunning
	}
	return StatusOK
}

// Summary is a one-line human description.
func (s Snapshot) Summary() string {
	if s.State != "OPEN" {
		return strings.ToLower(s.State)
	}
	var parts []string
	switch s.CI {
	case CIFailure:
		parts = append(parts, fmt.Sprintf("CI failed: %s", checkNames(s.FailedChecks)))
	case CIPending:
		parts = append(parts, fmt.Sprintf("CI running (%d pending)", len(s.PendingChecks)))
	case CISuccess:
		parts = append(parts, "CI green")
	}
	if s.Mergeable == "CONFLICTING" {
		parts = append(parts, "merge conflict")
	}
	if s.Unresolved > 0 {
		parts = append(parts, fmt.Sprintf("%d unresolved threads", s.Unresolved))
	}
	switch s.ReviewDecision {
	case "APPROVED":
		parts = append(parts, "approved")
	case "CHANGES_REQUESTED":
		parts = append(parts, "changes requested")
	}
	if s.Draft {
		parts = append(parts, "draft")
	}
	return strings.Join(parts, ", ")
}

func checkNames(cs []Check) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

// Change kinds produced by Diff. Each one becomes an event, and the worker
// decides which follow-up tasks (Slack, later review resolver / agent) it needs.
const (
	ChangeCIFailed         = "ci_failed"
	ChangeCIPassed         = "ci_passed"
	ChangeConflict         = "conflict"
	ChangeConflictResolved = "conflict_resolved"
	ChangeChangesRequested = "changes_requested"
	ChangeApproved         = "approved"
	ChangeNewComments      = "new_comments"
	ChangePushed           = "pushed"
	ChangeMerged           = "merged"
	ChangeClosed           = "closed"
)

// Change is one meaningful difference between two snapshots.
type Change struct {
	Kind    string  `json:"kind"`
	Message string  `json:"message"`
	Checks  []Check `json:"checks,omitempty"` // for ci_failed
}

// Diff lists what changed from prev to cur. prev is nil on the first check;
// then only problems are reported, so adding a broken PR still alerts you
// without announcing every green one.
func Diff(prev *Snapshot, cur Snapshot) []Change {
	first := prev == nil
	if first {
		prev = &Snapshot{}
	}
	var out []Change
	add := func(kind, msg string) { out = append(out, Change{Kind: kind, Message: msg}) }

	if cur.State != prev.State && !first {
		switch cur.State {
		case "MERGED":
			add(ChangeMerged, "merged")
			return out
		case "CLOSED":
			add(ChangeClosed, "closed without merging")
			return out
		}
	}
	if cur.State != "OPEN" {
		return out
	}

	if !first && cur.HeadSHA != prev.HeadSHA && prev.HeadSHA != "" {
		add(ChangePushed, "new commit "+short(cur.HeadSHA))
	}

	// A check counts as newly failed if it wasn't failing before, or if a new
	// commit was pushed (a re-run that fails again is still news).
	var newFailed []Check
	for _, c := range cur.FailedChecks {
		if cur.HeadSHA != prev.HeadSHA || !slices.ContainsFunc(prev.FailedChecks, func(p Check) bool { return p.Name == c.Name }) {
			newFailed = append(newFailed, c)
		}
	}
	if len(newFailed) > 0 {
		out = append(out, Change{Kind: ChangeCIFailed, Message: "CI failed: " + checkNames(newFailed), Checks: newFailed})
	} else if !first && cur.CI == CISuccess && prev.CI != CISuccess {
		add(ChangeCIPassed, "CI passed")
	}

	if cur.Mergeable == "CONFLICTING" && prev.Mergeable != "CONFLICTING" {
		add(ChangeConflict, "merge conflict with the base branch")
	} else if prev.Mergeable == "CONFLICTING" && cur.Mergeable == "MERGEABLE" {
		add(ChangeConflictResolved, "conflict resolved")
	}

	if cur.ReviewDecision != prev.ReviewDecision {
		switch cur.ReviewDecision {
		case "CHANGES_REQUESTED":
			add(ChangeChangesRequested, "changes requested")
		case "APPROVED":
			if !first {
				add(ChangeApproved, "approved")
			}
		}
	}

	if cur.Unresolved > prev.Unresolved {
		add(ChangeNewComments, fmt.Sprintf("%d unresolved review threads (was %d)", cur.Unresolved, prev.Unresolved))
	}
	return out
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
