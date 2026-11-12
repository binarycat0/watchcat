package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/binarycat0/watchcat/internal/config"
	"github.com/binarycat0/watchcat/internal/secrets"
)

// cmdSecret manages secrets in the OS keychain:
//
//	watchcat secret set slack-token     prompts without echo (or reads stdin)
//	watchcat secret delete slack-token
//	watchcat secret list
//
// Values are never taken as arguments, so they don't end up in shell history.
func cmdSecret(e env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: watchcat secret set|delete <name> | list")
	}
	store := secrets.Keyring{}
	switch args[0] {
	case "list", "ls":
		for _, st := range e.secretStatuses() {
			line := fmt.Sprintf("%-12s %-26s %s", st.Name, st.Source, st.Masked)
			if st.Err != "" {
				line += "  (keychain error: " + st.Err + ")"
			}
			fmt.Println(line)
		}
		return nil
	case "set", "delete", "rm":
	default:
		return fmt.Errorf("unknown secret command %q", args[0])
	}

	if len(args) != 2 {
		return fmt.Errorf("usage: watchcat secret %s <name>", args[0])
	}
	s, err := secrets.Lookup(args[1])
	if err != nil {
		return err
	}

	if args[0] != "set" {
		if err := store.Delete(s.Name); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return err
		}
		fmt.Println("deleted", s.Name, "from the keychain")
		return restartHint()
	}

	value, err := readSecret(s)
	if err != nil {
		return err
	}
	if err := store.Set(s.Name, value); err != nil {
		return fmt.Errorf("save to keychain: %w", err)
	}
	fmt.Printf("saved %s to the keychain (service %q)\n", s.Name, secrets.ServiceName(s.Name))
	if s.Name == secrets.SlackToken {
		if removed, err := config.RemovePlaintextToken(e.paths); err != nil {
			return err
		} else if removed {
			fmt.Println("removed the plaintext slack.bot_token from", e.paths.Config)
		}
	}
	return restartHint()
}

// readSecret prompts without echo on a terminal, or reads one line from
// stdin when piped (e.g. `pbpaste | watchcat secret set slack-token`).
func readSecret(s secrets.Secret) (string, error) {
	var value string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "%s: ", s.Desc)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		value = string(b)
	} else {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read secret from stdin: %w", err)
		}
		value = line
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("empty value, nothing saved")
	}
	return value, nil
}

func restartHint() error {
	fmt.Println("restart the daemon to use it: watchcat service restart")
	return nil
}
