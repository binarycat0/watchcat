// Package service installs watchcat as a per-user background service:
// a launchd agent on macOS, a systemd user unit on Linux. None of this needs
// the daemon to be running.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"text/template"
)

const (
	Label    = "io.github.binarycat0.watchcat" // launchd label
	UnitName = "watchcat.service"              // systemd unit
)

// Spec is everything needed to render a service definition.
type Spec struct {
	Executable string            // absolute path to the watchcat binary
	Args       []string          // e.g. ["daemon"]
	Env        map[string]string // PATH etc.; launchd starts with a bare environment
	LogPath    string
}

// Manager is implemented once per init system.
type Manager interface {
	Name() string
	Path() string // where the definition file lives
	Render(Spec) ([]byte, error)
	Install(Spec) error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error // picks up a new binary after an upgrade
	Status() (string, error)
}

// ErrUnsupported is returned on platforms with neither launchd nor systemd.
var ErrUnsupported = errors.New("service management supports macOS (launchd) and Linux (systemd) only")

// ForPlatform picks the manager for the running OS.
func ForPlatform() (Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "darwin":
		return &Launchd{Dir: filepath.Join(home, "Library", "LaunchAgents"), UID: os.Getuid()}, nil
	case "linux":
		dir := filepath.Join(home, ".config", "systemd", "user")
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "systemd", "user")
		}
		return &Systemd{Dir: dir}, nil
	}
	return nil, ErrUnsupported
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// run executes a command and folds its output into the error, so failures
// from launchctl/systemctl are readable.
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w: %s", name, args, err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

// ---- launchd (macOS) ----

type Launchd struct {
	Dir string
	UID int
}

func (l *Launchd) Name() string   { return "launchd" }
func (l *Launchd) Path() string   { return filepath.Join(l.Dir, Label+".plist") }
func (l *Launchd) domain() string { return "gui/" + strconv.Itoa(l.UID) }

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Spec.Executable}}</string>
{{- range .Spec.Args}}
		<string>{{.}}</string>
{{- end}}
	</array>
{{- if .Spec.Env}}
	<key>EnvironmentVariables</key>
	<dict>
{{- range $k, $v := .Spec.Env}}
		<key>{{$k}}</key>
		<string>{{$v}}</string>
{{- end}}
	</dict>
{{- end}}
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>{{.Spec.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.Spec.LogPath}}</string>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`))

// Render produces the plist. text/template does not XML-escape, so values go
// through xmlEscape first.
func (l *Launchd) Render(s Spec) ([]byte, error) {
	esc := Spec{Executable: xmlEscape(s.Executable), LogPath: xmlEscape(s.LogPath), Env: map[string]string{}}
	for _, a := range s.Args {
		esc.Args = append(esc.Args, xmlEscape(a))
	}
	for k, v := range s.Env {
		esc.Env[xmlEscape(k)] = xmlEscape(v)
	}
	var buf bytes.Buffer
	err := plistTmpl.Execute(&buf, struct {
		Label string
		Spec  Spec
	}{Label, esc})
	return buf.Bytes(), err
}

func (l *Launchd) Install(s Spec) error {
	b, err := l.Render(s)
	if err != nil {
		return err
	}
	// Unload a previous version first so launchd picks up the new plist.
	l.Stop()
	return writeFile(l.Path(), b)
}

func (l *Launchd) Uninstall() error {
	l.Stop()
	err := os.Remove(l.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l *Launchd) Start() error {
	if _, err := os.Stat(l.Path()); err != nil {
		return fmt.Errorf("not installed: run `watchcat service install` first")
	}
	if l.loaded() {
		_, err := run("launchctl", "kickstart", l.domain()+"/"+Label)
		return err
	}
	_, err := run("launchctl", "bootstrap", l.domain(), l.Path())
	return err
}

// Restart kills and relaunches the agent (kickstart -k), loading it first if
// needed.
func (l *Launchd) Restart() error {
	if !l.loaded() {
		return l.Start()
	}
	_, err := run("launchctl", "kickstart", "-k", l.domain()+"/"+Label)
	return err
}

func (l *Launchd) Stop() error {
	if !l.loaded() {
		return nil
	}
	_, err := run("launchctl", "bootout", l.domain()+"/"+Label)
	return err
}

func (l *Launchd) loaded() bool {
	return exec.Command("launchctl", "print", l.domain()+"/"+Label).Run() == nil
}

func (l *Launchd) Status() (string, error) {
	if _, err := os.Stat(l.Path()); err != nil {
		return "not installed", nil
	}
	if !l.loaded() {
		return "installed, not loaded", nil
	}
	return "loaded", nil
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			buf.WriteString("&amp;")
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		case '"':
			buf.WriteString("&quot;")
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// ---- systemd (Linux) ----

type Systemd struct {
	Dir string
}

func (s *Systemd) Name() string { return "systemd" }
func (s *Systemd) Path() string { return filepath.Join(s.Dir, UnitName) }

var unitTmpl = template.Must(template.New("unit").Funcs(template.FuncMap{"q": strconv.Quote}).Parse(`[Unit]
Description=watchcat PR monitor
After=network-online.target

[Service]
ExecStart={{q .Executable}}{{range .Args}} {{q .}}{{end}}
{{- range $k, $v := .Env}}
Environment={{q (printf "%s=%s" $k $v)}}
{{- end}}
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`))

func (s *Systemd) Render(spec Spec) ([]byte, error) {
	var buf bytes.Buffer
	err := unitTmpl.Execute(&buf, spec)
	return buf.Bytes(), err
}

func (s *Systemd) Install(spec Spec) error {
	b, err := s.Render(spec)
	if err != nil {
		return err
	}
	if err := writeFile(s.Path(), b); err != nil {
		return err
	}
	_, err = run("systemctl", "--user", "daemon-reload")
	return err
}

func (s *Systemd) Uninstall() error {
	run("systemctl", "--user", "disable", "--now", UnitName)
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := run("systemctl", "--user", "daemon-reload")
	return err
}

func (s *Systemd) Start() error {
	_, err := run("systemctl", "--user", "enable", "--now", UnitName)
	return err
}

func (s *Systemd) Restart() error {
	_, err := run("systemctl", "--user", "restart", UnitName)
	return err
}

func (s *Systemd) Stop() error {
	_, err := run("systemctl", "--user", "stop", UnitName)
	return err
}

func (s *Systemd) Status() (string, error) {
	if _, err := os.Stat(s.Path()); err != nil {
		return "not installed", nil
	}
	out, _ := exec.Command("systemctl", "--user", "is-active", UnitName).Output()
	return string(bytes.TrimSpace(out)), nil
}
