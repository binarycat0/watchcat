package main

import (
	"fmt"
	"os"

	"github.com/binarycat0/watchcat/internal/health"
)

// cmdDoctor checks the setup without needing the daemon. Each check prints
// ok/warn/fail; the command fails only if something is broken.
func cmdDoctor(e env) error {
	failed := false
	report := func(level, what, detail string) {
		fmt.Printf("[%-4s] %-10s %s\n", level, what, detail)
		if level == "fail" {
			failed = true
		}
	}

	if _, err := os.Stat(e.paths.Config); err != nil {
		report("warn", "config", "missing, run `watchcat init` ("+e.paths.Config+")")
	} else {
		report("ok", "config", e.paths.Config)
	}

	if _, err := os.Stat(e.paths.DB); err != nil {
		report("warn", "database", "not created yet; the daemon creates it on first start")
	} else {
		report("ok", "database", e.paths.DB)
	}

	c, cancel := ctx()
	defer cancel()
	if st, err := e.client().Status(c); err != nil {
		report("warn", "daemon", fmt.Sprintf("not reachable at %s: %v", e.cfg.Listen, err))
	} else {
		report("ok", "daemon", fmt.Sprintf("pid %d, version %s, %d PRs", st.PID, st.Version, st.PRs))
	}

	fmt.Println()
	rep := e.localHealth(c)
	fmt.Printf("tool health from this shell: %s\n", rep.Status)
	fmt.Println("(the service may see a different environment; `watchcat health` asks the daemon)")
	printReport(rep)
	if rep.Status == health.Fail {
		failed = true
	}

	if failed {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}
