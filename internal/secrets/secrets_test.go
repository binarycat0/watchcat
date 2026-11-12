package secrets

import (
	"errors"
	"testing"
)

type mapStore map[string]string

func (m mapStore) Get(n string) (string, error) {
	if v, ok := m[n]; ok {
		return v, nil
	}
	return "", ErrNotFound
}
func (m mapStore) Set(n, v string) error { m[n] = v; return nil }
func (m mapStore) Delete(n string) error { delete(m, n); return nil }

type brokenStore struct{ mapStore }

func (brokenStore) Get(string) (string, error) { return "", errors.New("keychain locked") }

func TestResolveOrder(t *testing.T) {
	s, _ := Lookup(SlackToken)
	store := mapStore{}

	if v, src, err := Resolve(store, s, ""); v != "" || src != FromNowhere || err != nil {
		t.Fatalf("empty: %q %q %v", v, src, err)
	}
	if v, src, _ := Resolve(store, s, "xoxb-legacy"); v != "xoxb-legacy" || src != FromConfig {
		t.Fatalf("legacy: %q %q", v, src)
	}
	store.Set(SlackToken, "xoxb-store")
	if v, src, _ := Resolve(store, s, "xoxb-legacy"); v != "xoxb-store" || src != FromStore {
		t.Fatalf("store wins over config: %q %q", v, src)
	}
	t.Setenv(s.Env, "xoxb-env")
	if v, src, _ := Resolve(store, s, ""); v != "xoxb-env" || src != FromEnv {
		t.Fatalf("env wins: %q %q", v, src)
	}
}

func TestResolveStoreError(t *testing.T) {
	s, _ := Lookup(SlackToken)
	v, src, err := Resolve(brokenStore{}, s, "xoxb-legacy")
	if err == nil || v != "xoxb-legacy" || src != FromConfig {
		t.Fatalf("store error should be reported with the fallback: %q %q %v", v, src, err)
	}
}

func TestLookupAndMask(t *testing.T) {
	if _, err := Lookup("nope"); err == nil {
		t.Fatal("unknown secret should fail")
	}
	if got := Mask("xoxb-1234567890-abcd"); got != "xoxb****abcd" {
		t.Fatalf("mask: %q", got)
	}
	if got := Mask("short"); got != "*****" {
		t.Fatalf("mask short: %q", got)
	}
}
