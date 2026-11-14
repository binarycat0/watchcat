package health

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/binarycat0/watchcat/internal/github"
)

// GH checks the GitHub CLI: installed, which hosts and accounts it is signed
// in to, whether each watched
// PR's host is among them, and read access to every watched repo.
type GH struct {
	Exec  Exec
	Repos []string // "host/owner/repo" of watched PRs
}

func (c GH) Run(ctx context.Context) Result {
	r := Result{Name: "gh", Title: "GitHub CLI", Required: true, Status: OK}
	if _, err := c.Exec.LookPath("gh"); err != nil {
		return r.set(Fail, "not installed: brew install gh, then gh auth login")
	}
	if out, err := c.Exec.Output(ctx, "gh", "--version"); err == nil {
		r.Version = strings.TrimPrefix(firstLine(out), "gh version ")
	}

	// gh exits non-zero when any account has a problem but still prints JSON.
	out, err := c.Exec.Output(ctx, "gh", "auth", "status", "--json", "hosts")
	accounts, perr := github.ParseAuthStatus(out)
	if perr != nil {
		if err == nil {
			err = perr
		}
		return r.set(Fail, "cannot read auth status: "+err.Error())
	}

	for _, a := range accounts {
		label := "account " + a.Host
		if !a.OK {
			r.addS(label, a.Login+": auth error: "+a.Error, Fail)
			continue
		}
		flags := []string{a.TokenSource}
		if a.Active {
			flags = append([]string{"active"}, flags...)
		}
		value := fmt.Sprintf("%s (%s); scopes: %s", a.Login, strings.Join(flags, ", "), a.Scopes)
		if a.Active && !slices.Contains(splitScopes(a.Scopes), "repo") {
			r.addS(label, value+"; missing repo scope: gh auth refresh -h "+a.Host+" -s repo", Fail)
			r.worsen(Warn)
			continue
		}
		r.addS(label, value, OK)
	}
	hosts := github.SignedInHosts(accounts)
	if len(hosts) == 0 {
		return r.set(Fail, "not signed in to any host: run gh auth login --hostname <host>")
	}
	r.Summary = "signed in to " + strings.Join(hosts, ", ")

	for _, host := range hosts {
		if orgs, err := c.Exec.Output(ctx, "gh", "api", "--hostname", host, "user/orgs", "--jq", ".[].login"); err == nil {
			if list := strings.Fields(string(orgs)); len(list) > 0 {
				r.add("orgs "+host, strings.Join(list, ", "))
			}
		}
	}

	// A second account, a missing org SSO authorisation or a removed fork can
	// block one repo while everything else works, so check each watched repo
	// on its own host.
	problems := 0
	for _, slug := range dedupe(c.Repos) {
		host, repo, ok := strings.Cut(slug, "/")
		if !ok {
			continue
		}
		if !slices.Contains(hosts, host) {
			r.addS("repo "+slug, "gh is not signed in to "+host+": gh auth login --hostname "+host, Fail)
			problems++
			continue
		}
		perm, err := c.Exec.Output(ctx, "gh", "api", "--hostname", host, "repos/"+repo,
			"--jq", ".permissions | to_entries | map(select(.value)) | map(.key) | join(\",\")")
		if err != nil {
			r.addS("repo "+slug, "no access: "+lastLine(err.Error()), Fail)
			problems++
			continue
		}
		r.addS("repo "+slug, "access: "+strings.TrimSpace(string(perm)), OK)
	}
	if problems > 0 {
		r.worsen(Fail)
		r.Summary += fmt.Sprintf("; %d watched repo(s) not readable", problems)
	} else if len(c.Repos) > 0 {
		r.Summary += fmt.Sprintf("; %d watched repo(s) readable", len(dedupe(c.Repos)))
	}
	return r
}

func splitScopes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.Trim(strings.TrimSpace(p), "'"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedupe(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(s)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
