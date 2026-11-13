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

// Setup checks watchcat's own installation: config file, database, the
// binary and its symlink, PATH, the background service, the daemon, and where
// each secret comes from. It is the one list of setup checks shared by
// `watchcat health`, the API and the web UI's Health page.
type Setup struct {
	Config  string // ~/.watchcat/config.json
	DB      string // ~/.watchcat/watchcat.db
	Bin     string // ~/.watchcat/bin/watchcat
	Link    string // ~/.local/bin/watchcat
	Service func() (manager, status string, err error)
	// Daemon describes the running daemon (pid, version, ...) or returns why
	// it can't be reached. Inside the daemon it describes itself.
	Daemon  func(ctx context.Context) (string, error)
	Secrets []SecretStatus
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

	if c.Config != "" {
		if _, err := os.Stat(c.Config); err != nil {
			warn("config", c.Config+" missing (run watchcat init)", "config missing")
		} else {
			r.add("config", c.Config)
		}
	}
	if c.DB != "" {
		if _, err := os.Stat(c.DB); err != nil {
			warn("database", c.DB+" not created yet; the daemon creates it on first start", "database not created yet")
		} else {
			r.add("database", c.DB)
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

	if c.Daemon != nil {
		if info, err := c.Daemon(ctx); err != nil {
			warn("daemon", "not reachable: "+err.Error(), "daemon not running")
		} else {
			r.add("daemon", info)
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
	return r
}
