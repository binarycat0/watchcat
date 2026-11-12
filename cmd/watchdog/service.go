package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/binarycat0/watchdog/internal/config"
	"github.com/binarycat0/watchdog/internal/service"
)

func cmdService(e env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: watchdog service install|uninstall|start|stop|restart|status")
	}
	m, err := service.ForPlatform()
	if err != nil {
		return err
	}
	switch args[0] {
	case "install":
		if _, err := config.Init(e.paths); err != nil {
			return err
		}
		spec, err := serviceSpec(e)
		if err != nil {
			return err
		}
		if err := m.Install(spec); err != nil {
			return err
		}
		fmt.Printf("installed %s service: %s\n", m.Name(), m.Path())
		fmt.Println("runs:", spec.Executable, "daemon")
		fmt.Println("next: `watchdog service start`")
		return nil
	case "uninstall":
		if err := m.Uninstall(); err != nil {
			return err
		}
		fmt.Println("uninstalled", m.Path())
		return nil
	case "start":
		if err := m.Start(); err != nil {
			return err
		}
		fmt.Printf("started; web UI at http://%s\n", e.cfg.Listen)
		return nil
	case "restart":
		if err := m.Restart(); err != nil {
			return err
		}
		fmt.Println("restarted")
		return nil
	case "stop":
		if err := m.Stop(); err != nil {
			return err
		}
		fmt.Println("stopped")
		return nil
	case "status":
		s, err := m.Status()
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s (%s)\n", m.Name(), s, m.Path())
		return nil
	}
	return fmt.Errorf("unknown service command %q", args[0])
}

// serviceSpec points the service at this binary's real path and carries over
// PATH (so it finds gh/opencode), USER and WATCHDOG_HOME.
func serviceSpec(e env) (service.Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Spec{}, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return service.Spec{}, err
	}
	// USER is part of each keychain entry's key; launchd doesn't always set it.
	envs := map[string]string{"PATH": os.Getenv("PATH"), "USER": os.Getenv("USER")}
	if h := os.Getenv("WATCHDOG_HOME"); h != "" {
		envs["WATCHDOG_HOME"] = h
	}
	return service.Spec{Executable: exe, Args: []string{"daemon"}, Env: envs, LogPath: e.paths.Log}, nil
}
