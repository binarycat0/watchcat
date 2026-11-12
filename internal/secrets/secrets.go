// Package secrets keeps tokens out of config.json, in the OS secret store,
// using the same convention as DAP's ~/.dap/get-secret.sh so either tool can
// read what the other stored:
//
//	macOS: security find-generic-password -a $USER -s <service> -w
//	Linux: secret-tool lookup service <service> account $USER
//
// Lookup order for each secret: its environment variable, then the secret
// store, then (deprecated) a plaintext value in config.json.
package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// ServicePrefix namespaces watchdog's entries: the Slack token is stored as
// service "watchdog-slack-token", account $USER.
const ServicePrefix = "watchdog-"

// SlackToken is the Slack bot token (xoxb-...).
const SlackToken = "slack-token"

// Secret describes one secret watchdog knows about.
type Secret struct {
	Name string // CLI name; the store's service is ServicePrefix+Name
	Env  string // environment variable that overrides the store
	Desc string
}

// ServiceName is the keychain service name for a secret name.
func ServiceName(name string) string { return ServicePrefix + name }

// Known lists every secret, so the CLI can validate names and list them.
var Known = []Secret{
	{Name: SlackToken, Env: "WATCHDOG_SLACK_TOKEN", Desc: "Slack bot token (xoxb-...)"},
}

// Lookup returns the Secret with this name.
func Lookup(name string) (Secret, error) {
	for _, s := range Known {
		if s.Name == name {
			return s, nil
		}
	}
	names := make([]string, len(Known))
	for i, s := range Known {
		names[i] = s.Name
	}
	return Secret{}, fmt.Errorf("unknown secret %q (known: %s)", name, strings.Join(names, ", "))
}

// ErrNotFound means the store has no value for the secret.
var ErrNotFound = errors.New("not found")

// Store is the OS secret store. An interface so tests can use a map.
type Store interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// Keyring is the real OS store: the `security` tool on macOS, `secret-tool`
// (libsecret) on Linux. Values are passed on stdin, never as arguments, so
// they don't show up in `ps`.
type Keyring struct{}

func account() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "watchdog"
}

func (Keyring) Get(name string) (string, error) {
	svc := ServiceName(name)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "find-generic-password", "-a", account(), "-s", svc, "-w")
	case "linux":
		cmd = exec.Command("secret-tool", "lookup", "service", svc, "account", account())
	default:
		return "", errUnsupported
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	v := strings.TrimRight(string(out), "\n")
	switch {
	case err == nil && v != "":
		return v, nil
	// security exits 44 for "not found"; secret-tool exits 1 with no output.
	case err == nil, strings.Contains(stderr.String(), "could not be found"),
		runtime.GOOS == "linux" && stderr.Len() == 0:
		return "", ErrNotFound
	}
	return "", storeErr(err, stderr.String())
}

func (Keyring) Set(name, value string) error {
	if strings.ContainsAny(value, "\n'") {
		return errors.New("value must be one line without single quotes")
	}
	svc := ServiceName(name)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// `security -i` reads commands from stdin, keeping the value out of argv.
		cmd = exec.Command("security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -a '%s' -s '%s' -l '%s' -w '%s'\n",
			account(), svc, svc, value))
	case "linux":
		cmd = exec.Command("secret-tool", "store", "--label="+svc, "service", svc, "account", account())
		cmd.Stdin = strings.NewReader(value)
	default:
		return errUnsupported
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return storeErr(err, string(out))
	}
	return nil
}

func (Keyring) Delete(name string) error {
	svc := ServiceName(name)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "delete-generic-password", "-a", account(), "-s", svc)
	case "linux":
		cmd = exec.Command("secret-tool", "clear", "service", svc, "account", account())
	default:
		return errUnsupported
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "could not be found") {
			return ErrNotFound
		}
		return storeErr(err, string(out))
	}
	return nil
}

var errUnsupported = errors.New("no secret store on this OS; use the environment variable")

func storeErr(err error, output string) error {
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return fmt.Errorf("%s not installed; use the environment variable instead", notFound.Name)
	}
	if msg := strings.TrimSpace(output); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// Source says where a resolved value came from.
const (
	FromEnv     = "env"
	FromStore   = "keychain"
	FromConfig  = "config.json (plaintext)"
	FromNowhere = "not set"
)

// Resolve finds a secret's value: environment, then store, then the legacy
// plaintext value from config.json (pass "" if there is none). A store error
// other than "not found" (e.g. locked keychain, no Secret Service on a
// headless Linux box) is returned alongside whatever fallback was found.
func Resolve(store Store, s Secret, legacy string) (value, source string, err error) {
	if v := os.Getenv(s.Env); v != "" {
		return v, FromEnv, nil
	}
	v, err := store.Get(s.Name)
	if err == nil && v != "" {
		return v, FromStore, nil
	}
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	if legacy != "" {
		return legacy, FromConfig, err
	}
	return "", FromNowhere, err
}

// Mask shows just enough of a secret to recognise it: the first and last 4
// characters with stars between ("xoxb****abcd"). Values under 12
// characters are fully hidden, since 8 visible characters would be most of it.
func Mask(v string) string {
	if len(v) < 12 {
		return strings.Repeat("*", len(v))
	}
	return v[:4] + "****" + v[len(v)-4:]
}
