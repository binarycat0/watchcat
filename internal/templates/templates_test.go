package templates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every default renders with its sample data, and every Def has a default.
func TestDefaultsRender(t *testing.T) {
	for _, d := range Defs {
		out, err := render(defaultSources, d.Name, d.sample())
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		if out == "" || strings.Contains(out, "<no value>") {
			t.Fatalf("%s rendered %q", d.Name, out)
		}
	}
	entries, _ := defaults.ReadDir("defaults")
	if len(entries) != len(Defs) {
		t.Fatalf("%d default files for %d templates", len(entries), len(Defs))
	}
}

func TestDefaultOutput(t *testing.T) {
	var s *Set // nil: defaults only
	got := s.Render(SlackChange, SampleChange)
	want := ":x: <https://github.com/acme/api/pull/42|acme/api#42> Add rate limiting to the public API\n" +
		"CI failed: test (ubuntu-latest), lint\n" +
		"• <https://github.com/acme/api/actions/runs/1/job/2|test (ubuntu-latest)>\n• lint"
	if got != want {
		t.Fatalf("slack_change:\n%s\nwant:\n%s", got, want)
	}
	got = s.Render(AgentReviewThreads, SampleReviewThreads)
	for _, part := range []string{
		"[watchcat] PR acme/api#42: Add rate limiting to the public API\n",
		"2 review conversation(s) are unresolved:\n\n1. internal/limit/limiter.go:27\n",
		"   @author: Good point, but anonymous calls have no user.\n   Maybe both?\n",
		"\n2. README.md\n",
		"Don't push until I confirm.\n\nRules: do not merge",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("review prompt lacks %q:\n%s", part, got)
		}
	}
	if !strings.HasSuffix(got, "what is left for me.") {
		t.Fatalf("trailing text: %q", got)
	}
}

func TestOverrideSaveRestore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	s := &Set{Dir: dir}

	i, err := s.Save(AgentFooter, "Be brief. {{.PR.Ref}}")
	if err != nil {
		t.Fatal(err)
	}
	if !i.Custom || i.Text != "Be brief. {{.PR.Ref}}\n" {
		t.Fatalf("after save: %+v", i)
	}
	// The partial applies to every prompt that includes it.
	if out := s.Render(AgentCIFailed, SampleCIFailed); !strings.HasSuffix(out, "Be brief. acme/api#42") {
		t.Fatalf("override not used:\n%s", out)
	}

	// Invalid text is refused and the file is left alone.
	for _, bad := range []string{"{{.PR.Nope}}", "{{if}}", "{{template \"missing\" .}}"} {
		if _, err := s.Save(AgentFooter, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("save %q: %v", bad, err)
		}
	}
	// Breaking a partial for its includers is caught too.
	if _, err := s.Save(AgentHeader, "{{.Threads}}"); err == nil || !strings.Contains(err.Error(), "breaks") {
		t.Fatalf("header using .Threads: %v", err)
	}

	if i, err = s.Restore(AgentFooter); err != nil || i.Custom || i.Text != Default(AgentFooter) {
		t.Fatalf("restore: %+v %v", i, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent_footer.tmpl")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}

	// Saving the default text doesn't create an override.
	if i, _ := s.Save(SlackRule, Default(SlackRule)); i.Custom {
		t.Fatal("saving the default created an override")
	}
	if _, err := s.Save("nope", "x"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
}

// A file broken on disk falls back to the default and is flagged.
func TestBrokenFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	s := &Set{Dir: dir}
	if err := os.WriteFile(filepath.Join(dir, "slack_rule.tmpl"), []byte("{{.Oops}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := s.Render(SlackRule, SampleRule); !strings.HasPrefix(out, ":x: <https://") {
		t.Fatalf("no fallback: %q", out)
	}
	i, _ := s.Get(SlackRule)
	if !i.Custom || i.Error == "" {
		t.Fatalf("broken file not flagged: %+v", i)
	}
}

func TestPreview(t *testing.T) {
	s := &Set{Dir: t.TempDir()}
	out, err := s.Preview(SlackChange, "{{.Kind}} {{.PR.Ref}} {{upper .PR.Repo}}")
	if err != nil || out != "ci_failed acme/api#42 ACME/API" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := s.Preview(SlackChange, "{{.Nope}}"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}
