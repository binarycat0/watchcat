package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestParseRef(t *testing.T) {
	// Table-driven tests are the Go equivalent of pytest.mark.parametrize.
	cases := []struct {
		in   string
		want PRRef
		ok   bool
	}{
		{"dremio/dremio#123", PRRef{"github.com", "dremio", "dremio", 123}, true},
		{"https://github.com/a-b/c.d/pull/7", PRRef{"github.com", "a-b", "c.d", 7}, true},
		{"https://github.com/a/b/pull/7/files", PRRef{"github.com", "a", "b", 7}, true},
		{"https://github.tools.sap/org/repo/pull/42", PRRef{"github.tools.sap", "org", "repo", 42}, true},
		{"github.tools.sap/org/repo#42", PRRef{"github.tools.sap", "org", "repo", 42}, true},
		{"https://GHE.Example.com/o/r/pull/1", PRRef{"ghe.example.com", "o", "r", 1}, true},
		{"a/b", PRRef{}, false},
		{"https://github.com/a/b/issues/7", PRRef{}, false},
		{"ftp://github.com/a/b/pull/7", PRRef{}, false},
	}
	for _, c := range cases {
		got, err := ParseRef(c.in, "")
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ParseRef(%q) = %+v, %v", c.in, got, err)
		}
	}
	if got, _ := ParseRef("o/r#1", "ghe.corp"); got.Host != "ghe.corp" {
		t.Errorf("short ref should use the default host, got %+v", got)
	}
	ref := PRRef{"ghe.corp", "o", "r", 1}
	if ref.String() != "ghe.corp/o/r#1" || ref.Slug() != "ghe.corp/o/r" || ref.URL() != "https://ghe.corp/o/r/pull/1" {
		t.Errorf("formatting: %s %s %s", ref.String(), ref.Slug(), ref.URL())
	}
	if (PRRef{"github.com", "o", "r", 1}).String() != "o/r#1" {
		t.Error("github.com refs stay short")
	}
}

func TestSameNumberOnTwoHosts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.AddPR(ctx, PRRef{"github.com", "o", "r", 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPR(ctx, PRRef{"ghe.corp", "o", "r", 1}); err != nil {
		t.Fatalf("same owner/repo#N on another host is a different PR: %v", err)
	}
	if p, err := s.FindPR(ctx, PRRef{"ghe.corp", "o", "r", 1}); err != nil || p.Host != "ghe.corp" {
		t.Fatalf("find: %+v %v", p, err)
	}
}

func TestStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	pr, err := s.AddPR(ctx, PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPR(ctx, PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate add: got %v, want ErrExists", err)
	}

	due, _ := s.DuePRs(ctx, time.Now())
	if len(due) != 1 {
		t.Fatalf("new PR should be due, got %d", len(due))
	}

	now := time.Now()
	snap := &Snapshot{State: "OPEN", CI: CISuccess}
	if err := s.RecordCheck(ctx, pr.ID, CheckResult{Status: StatusOK, Summary: "green", Snapshot: snap}, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	due, _ = s.DuePRs(ctx, time.Now())
	if len(due) != 0 {
		t.Fatalf("checked PR should not be due, got %d", len(due))
	}
	got, _ := s.GetPR(ctx, pr.ID)
	if got.Status != StatusOK || got.LastChecked == nil || got.Snapshot == nil || got.Snapshot.CI != CISuccess {
		t.Fatalf("unexpected PR after check: %+v", got)
	}

	evs, _ := s.Events(ctx, pr.ID, 10, 0)
	if len(evs) != 2 || evs[0].Kind != "status" {
		t.Fatalf("expected added+status events, got %+v", evs)
	}
	if older, _ := s.Events(ctx, pr.ID, 1, 1); len(older) != 1 || older[0].ID != evs[1].ID {
		t.Fatalf("offset 1 should return the older event, got %+v", older)
	}

	s.SetNextCheck(ctx, pr.ID, time.Now().Add(-time.Second))
	s.SetPaused(ctx, pr.ID, true)
	if due, _ = s.DuePRs(ctx, time.Now()); len(due) != 0 {
		t.Fatal("paused PR must not be due")
	}

	if err := s.RemovePR(ctx, pr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePR(ctx, pr.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: got %v", err)
	}
	if evs, _ = s.Events(ctx, pr.ID, 10, 0); len(evs) != 0 {
		t.Fatal("events should cascade-delete with the PR")
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, _ := Open(path)
	s.AddPR(context.Background(), PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	s.Close()
	s, err := Open(path) // migrations must be idempotent
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	prs, _ := s.ListPRs(context.Background())
	if len(prs) != 1 {
		t.Fatalf("got %d PRs after reopen", len(prs))
	}
}
