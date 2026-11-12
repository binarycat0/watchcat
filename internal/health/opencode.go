package health

import "context"

// Opencode checks that the opencode CLI is installed. You run it yourself in
// a terminal (one process can hold several sessions), so there is no server
// for watchdog to reach. Optional until agent hand-off uses it.
type Opencode struct {
	Exec Exec
}

func (c Opencode) Run(ctx context.Context) Result {
	r := Result{Name: "opencode", Title: "opencode", Required: false, Status: OK}
	path, err := c.Exec.LookPath("opencode")
	if err != nil {
		return r.set(Fail, "not installed (needed for agent hand-off)")
	}
	if out, err := c.Exec.Output(ctx, "opencode", "--version"); err == nil {
		r.Version = firstLine(out)
	}
	r.add("path", path)
	r.Summary = "installed"
	return r
}
