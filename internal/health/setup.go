package health

import (
	"context"
	"os"
	"path/filepath"
	"slices"
)

// SecretStatus is one secret as the Setup check reports it. Masked shows only
// the first and last 4 characters.
type SecretStatus struct {
	Name   string `json:"name"`
	Source string `json:"source"` // env, keychain, config.json (plaintext), not set
	Masked string `json:"masked,omitempty"`
	Err    string `json:"error,omitempty"`
	Hint   string `json:"hint,omitempty"` // command to set it
}

// Setup checks watchcat's own installation: the binary and its symlink,
// PATH, the background service, and where each secret comes from.
type Setup struct {
	Bin      string // ~/.watchcat/bin/watchcat
	Link     string // ~/.local/bin/watchcat
	Service  func() (manager, status string, err error)
	Secrets  []SecretStatus
	LegacyIn string // old data directory still present, if any
	// OtherWatchers lists other schedulers that act on PRs (e.g. the old
	// Python pr-watchdog's launchd job); they would act regardless of
	// watchcat's dry-run settings.
	OtherWatchers []string
}

func (c Setup) Run(ctx context.Context) Result {
	r := Result{Name: "setup", Title: "Setup", Required: true, Status: OK, Summary: "installed as expected"}
	warn := func(label, value, summary string) {
		r.addS(label, value, Fail)
		r.worsen(Warn)
		if r.Summary == "installed as expected" {
			r.Summary = summary
		}
	}

	if _, err := os.Stat(c.Bin); err != nil {
		warn("binary", c.Bin+" missing", "not installed with make install")
	} else {
		r.add("binary", c.Bin)
	}
	if target, err := filepath.EvalSymlinks(c.Link); err != nil {
		warn("symlink", c.Link+" missing (run make install)", "symlink missing")
	} else if want, _ := filepath.EvalSymlinks(c.Bin); target != want {
		warn("symlink", c.Link+" points to "+target, "symlink points elsewhere")
	} else {
		r.add("symlink", c.Link+" -> "+c.Bin)
	}
	if dir := filepath.Dir(c.Link); slices.Contains(filepath.SplitList(os.Getenv("PATH")), dir) {
		r.add("PATH", "includes "+dir)
	} else {
		warn("PATH", dir+` not on PATH: export PATH="$HOME/.local/bin:$PATH"`, dir+" is not on PATH")
	}

	if c.Service != nil {
		name, status, err := c.Service()
		switch {
		case err != nil:
			warn("service", err.Error(), "service: "+err.Error())
		case status == "loaded" || status == "active":
			r.add("service", name+": "+status)
		default:
			warn("service", name+": "+status, "service "+status)
		}
	}

	for _, s := range c.Secrets {
		label := "secret " + s.Name
		switch {
		case s.Err != "":
			warn(label, s.Source+"; keychain error: "+s.Err, "keychain error")
		case s.Source == "not set":
			warn(label, "not set: "+s.Hint, s.Name+" not set")
		case s.Source == "config.json (plaintext)":
			warn(label, "plaintext in config.json "+s.Masked+": "+s.Hint, s.Name+" stored in plaintext")
		default:
			r.add(label, s.Source+" "+s.Masked)
		}
	}
	for _, o := range c.OtherWatchers {
		warn("other watcher", o, "another PR watcher is still scheduled")
	}
	if c.LegacyIn != "" {
		warn("old data", c.LegacyIn+" still exists; watchcat init moves it", "old data directory left over")
	}
	return r
}
