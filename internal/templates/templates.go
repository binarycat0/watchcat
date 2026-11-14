// Package templates renders the notifications and AI agent prompts watchcat
// sends. Defaults are embedded in the binary; a file with the same name in
// the templates directory (~/.watchcat/templates/<name>.tmpl) overrides one.
// Deleting that file restores the default. Files are read on every render,
// so edits apply without a restart.
//
// Templates use Go text/template syntax: https://pkg.go.dev/text/template.
package templates

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed defaults/*.tmpl
var defaults embed.FS

// Template names.
const (
	NotifyChange       = "notify_change"
	NotifyRule         = "notify_rule"
	AgentHeader        = "agent_header"
	AgentFooter        = "agent_footer"
	AgentCIFailed      = "agent_ci_failed"
	AgentReviewThreads = "agent_review_threads"
)

// Groups, as shown in the UI.
const (
	GroupNotify = "notify"
	GroupAgent  = "agent"
)

// Var documents one value a template can use.
type Var struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// Def describes one template.
type Def struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Group       string   `json:"group"`
	Description string   `json:"description"`
	Vars        []Var    `json:"vars"`
	Uses        []string `json:"uses,omitempty"` // partials it includes with {{template "name" .}}
	sample      func() any
}

var prVars = []Var{
	{".PR.Ref", "owner/repo#123"},
	{".PR.URL", "link to the PR"},
	{".PR.Title", "PR title"},
	{".PR.Repo", "owner/repo"},
	{".PR.Number", "PR number"},
	{".PR.HeadSHA", "head commit"},
	{".PR.Base", "base branch (main if unknown)"},
}

func vars(extra ...Var) []Var { return append(append([]Var{}, prVars...), extra...) }

var agentVars = vars(
	Var{".FailedChecks", "agent_ci_failed only: failing checks, each with .Name and .URL"},
	Var{".Logs", "agent_ci_failed only: tail of the failed Actions logs"},
	Var{".Threads", "agent_review_threads only: unresolved conversations"},
)

// Defs lists every template, in the order the UI shows them.
var Defs = []Def{
	{
		Name: NotifyChange, Title: "PR change", Group: GroupNotify,
		Description: "Notification when a watched PR changes.",
		Vars: vars(
			Var{".Kind", "ci_failed, ci_passed, conflict, conflict_resolved, changes_requested, approved, new_comments, pushed, merged, closed"},
			Var{".Message", "what changed, e.g. \"CI failed: test\""},
			Var{".Checks", "newly failed checks (ci_failed only), each with .Name and .URL"},
		),
		sample: func() any { return SampleChange },
	},
	{
		Name: NotifyRule, Title: "Workflow rule fired", Group: GroupNotify,
		Description: "Notification when a workflow rule fires (in dry-run or on).",
		Vars: vars(
			Var{".Rule", "merge, review_threads, ci_failed, needs_review"},
			Var{".Action", "what the rule does, e.g. \"merge the PR\""},
			Var{".DryRun", "true when the rule is in dry-run mode"},
			Var{".FailedChecks", "failing checks, each with .Name and .URL"},
			Var{".Unresolved", "number of unresolved review conversations"},
		),
		sample: func() any { return SampleRule },
	},
	{
		Name: AgentCIFailed, Title: "CI failed", Group: GroupAgent,
		Description: "Prompt asking the agent to triage a CI failure (ci_failed rule).",
		Vars: vars(
			Var{".FailedChecks", "failing checks, each with .Name and .URL"},
			Var{".Logs", "tail of each failed GitHub Actions run's log; may be empty"},
		),
		Uses:   []string{AgentHeader, AgentFooter},
		sample: func() any { return SampleCIFailed },
	},
	{
		Name: AgentReviewThreads, Title: "Review conversations", Group: GroupAgent,
		Description: "Prompt asking the agent to go through unresolved review conversations (review_threads rule).",
		Vars: vars(
			Var{".Threads", "unresolved conversations, each with .N (1, 2, ...), .Path, .Line, .Location (path:line), .URL and .Comments"},
			Var{"(comment) .Author .Body .URL", "one comment in a thread"},
		),
		Uses:   []string{AgentHeader, AgentFooter},
		sample: func() any { return SampleReviewThreads },
	},
	{
		Name: AgentHeader, Title: "Prompt header", Group: GroupAgent,
		Description: "Shared start of every agent prompt, included with {{template \"agent_header\" .}}.",
		Vars:        agentVars,
		sample:      func() any { return SampleCIFailed },
	},
	{
		Name: AgentFooter, Title: "Prompt footer", Group: GroupAgent,
		Description: "Shared end of every agent prompt: the ground rules. Included with {{template \"agent_footer\" .}}.",
		Vars:        agentVars,
		sample:      func() any { return SampleCIFailed },
	},
}

// Lookup returns the definition of a template.
func Lookup(name string) (Def, bool) {
	for _, d := range Defs {
		if d.Name == name {
			return d, true
		}
	}
	return Def{}, false
}

// Format is how a notification provider writes markup. Templates call
// {{link URL TEXT}} and the provider decides what a link looks like.
// Providers also escape text values before rendering, so templates stay
// provider-agnostic.
type Format struct {
	Name string
	Link func(url, text string) string
}

// PlainText writes links as "text (url)". Agent prompts always use it.
var PlainText = Format{Name: "text", Link: func(url, text string) string {
	if url == "" {
		return text
	}
	return text + " (" + url + ")"
}}

// ErrUnknown is returned for a template name that doesn't exist.
var ErrUnknown = errors.New("unknown template")

// ErrInvalid wraps a parse or render error in a template being saved or
// previewed.
var ErrInvalid = errors.New("invalid template")

var funcs = template.FuncMap{
	// names joins check names: {{names .FailedChecks}} -> "test, lint".
	"names": func(cs []Check) string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Name
		}
		return strings.Join(out, ", ")
	},
	// indent puts pad after every line break: {{indent "   " .Body}}.
	"indent": func(pad, s string) string {
		return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n"+pad)
	},
	// link is replaced per render by the provider's Format.Link.
	"link":  PlainText.Link,
	"join":  strings.Join,
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"trim":  strings.TrimSpace,
}

// Default returns the embedded default text of a template.
func Default(name string) string {
	b, err := defaults.ReadFile("defaults/" + name + ".tmpl")
	if err != nil {
		panic("templates: no default for " + name) // every Def has one; see the tests
	}
	return string(b)
}

// Set renders templates, using the overrides in Dir. A nil Set, or one with
// an empty Dir, uses only the defaults.
type Set struct {
	Dir string
	Log *slog.Logger
	// Notify is the notification provider's format, used for notification
	// templates (including previews). Zero means PlainText.
	Notify Format
}

// format returns the Format a template renders with.
func (s *Set) format(d Def) Format {
	if d.Group == GroupNotify && s != nil && s.Notify.Link != nil {
		return s.Notify
	}
	return PlainText
}

func (s *Set) path(name string) string { return filepath.Join(s.Dir, name+".tmpl") }

// source returns the override if there is one, else the default.
func (s *Set) source(name string) (text string, custom bool, err error) {
	if s == nil || s.Dir == "" {
		return Default(name), false, nil
	}
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return Default(name), false, nil
	}
	if err != nil {
		return Default(name), false, err
	}
	return string(b), true, nil
}

type sources func(name string) string

func (s *Set) current() sources {
	return func(name string) string {
		text, _, err := s.source(name)
		if err != nil {
			s.warn("reading template, using the default", name, err)
		}
		return text
	}
}

func defaultSources(name string) string { return Default(name) }

// with returns src with name's text replaced.
func (src sources) with(name, text string) sources {
	return func(n string) string {
		if n == name {
			return text
		}
		return src(n)
	}
}

// render parses name (and the partials it uses) from src and executes it.
func render(src sources, name string, data any, f Format) (string, error) {
	d, ok := Lookup(name)
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknown, name)
	}
	t := template.New(name).Funcs(funcs).Funcs(template.FuncMap{"link": f.Link}).Option("missingkey=error")
	for _, p := range d.Uses {
		if _, err := t.New(p).Parse(trimSource(src(p))); err != nil {
			return "", err
		}
	}
	if _, err := t.Parse(trimSource(src(name))); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), " \t\n"), nil
}

// trimSource drops the file's final line break, so a partial included
// mid-text doesn't add a blank line and editors can keep their newline.
func trimSource(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// Render renders a template with data. If the override fails to render, it
// logs why and falls back to the defaults, so a broken file never stops a
// message from going out.
func (s *Set) Render(name string, data any) string {
	d, ok := Lookup(name)
	if !ok {
		return fmt.Sprintf("watchcat: unknown template %s", name)
	}
	return s.RenderAs(s.format(d), name, data)
}

// RenderAs is Render with a given format: a notification provider renders
// its messages with its own format.
func (s *Set) RenderAs(f Format, name string, data any) string {
	out, err := render(s.current(), name, data, f)
	if err == nil {
		return out
	}
	s.warn("template failed, using the default", name, err)
	out, err = render(defaultSources, name, data, f)
	if err != nil {
		// Defaults are tested against every data type; this is a bug.
		return fmt.Sprintf("watchcat: template %s failed: %v", name, err)
	}
	return out
}

func (s *Set) warn(msg, name string, err error) {
	if s != nil && s.Log != nil {
		s.Log.Warn(msg, "template", name, "err", err)
	}
}

// Info is a template with its current text, for the UI.
type Info struct {
	Def
	Text    string `json:"text"`
	Default string `json:"default"`
	Custom  bool   `json:"custom"`          // an override file exists
	Path    string `json:"path,omitempty"`  // where the override lives
	Error   string `json:"error,omitempty"` // the current text doesn't render
}

// List returns every template with its current text.
func (s *Set) List() []Info {
	out := make([]Info, 0, len(Defs))
	for _, d := range Defs {
		i, _ := s.Get(d.Name)
		out = append(out, i)
	}
	return out
}

// Get returns one template with its current text.
func (s *Set) Get(name string) (Info, error) {
	d, ok := Lookup(name)
	if !ok {
		return Info{}, fmt.Errorf("%w %q", ErrUnknown, name)
	}
	text, custom, err := s.source(name)
	i := Info{Def: d, Text: text, Default: Default(name), Custom: custom}
	if s != nil && s.Dir != "" {
		i.Path = s.path(name)
	}
	if err != nil {
		i.Error = err.Error()
	} else if err := s.check(name, text); err != nil {
		i.Error = err.Error()
	}
	return i, nil
}

// check renders name and every template that includes it with sample data,
// using text for name.
func (s *Set) check(name, text string) error {
	src := s.current().with(name, text)
	for _, d := range Defs {
		if d.Name != name && !contains(d.Uses, name) {
			continue
		}
		if _, err := render(src, d.Name, d.sample(), s.format(d)); err != nil {
			if d.Name != name {
				return fmt.Errorf("%w: breaks %s: %v", ErrInvalid, d.Name, err)
			}
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	return nil
}

// Preview renders text as template name with sample data.
func (s *Set) Preview(name, text string) (string, error) {
	d, ok := Lookup(name)
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknown, name)
	}
	if err := s.check(name, text); err != nil {
		return "", err
	}
	return render(s.current().with(name, text), d.Name, d.sample(), s.format(d))
}

// Save validates text and writes it as name's override. Saving the default
// text removes the override instead, so later default changes still apply.
func (s *Set) Save(name, text string) (Info, error) {
	if _, ok := Lookup(name); !ok {
		return Info{}, fmt.Errorf("%w %q", ErrUnknown, name)
	}
	if s == nil || s.Dir == "" {
		return Info{}, errors.New("no templates directory configured")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if err := s.check(name, text); err != nil {
		return Info{}, err
	}
	if trimSource(text) == trimSource(Default(name)) {
		return s.Restore(name)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Info{}, err
	}
	// Write then rename, so a render never sees half a file.
	tmp := s.path(name) + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmp, s.path(name)); err != nil {
		return Info{}, err
	}
	return s.Get(name)
}

// Restore deletes name's override, going back to the default.
func (s *Set) Restore(name string) (Info, error) {
	if _, ok := Lookup(name); !ok {
		return Info{}, fmt.Errorf("%w %q", ErrUnknown, name)
	}
	if s != nil && s.Dir != "" {
		if err := os.Remove(s.path(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Info{}, err
		}
	}
	return s.Get(name)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
