# Contributing

This section covers the repository, building and testing, the docs site and
releases. [How it works](architecture.md) explains the internals and the code
layout.

## Repository

- Source: [github.com/binarycat0/watchcat](https://github.com/binarycat0/watchcat), default branch `main`
- Bugs and ideas: [Issues](https://github.com/binarycat0/watchcat/issues)
- Releases: [Releases](https://github.com/binarycat0/watchcat/releases)
- Docs site: [tools.binaryc.at/watchcat](https://tools.binaryc.at/watchcat/)

```bash
git clone https://github.com/binarycat0/watchcat.git
cd watchcat
make test
```

Work on a branch and open a pull request against `main`. Run `make test`
before pushing, and `make docs` if you changed `README.md`, `doc/` or
`mkdocs.yml`.

| Path | |
|---|---|
| `cmd/watchcat/` | CLI entry point and subcommands |
| `internal/` | daemon, store, providers, API; see [How it works](architecture.md#code-layout) |
| `web/ui/` | web UI (React + TypeScript) |
| `doc/`, `mkdocs.yml`, `utils/mkdocs/` | this docs site |
| `install.sh` | the curl installer, also published on the docs site |
| `.github/workflows/` | CI, see below |

## Prerequisites

- `go` 1.26+
- `node` 20+ and `npm` (web UI)
- `docker` or `podman` (docs site)

## Build and test

```bash
make install     # build UI + binary, install like install.sh does
make test        # go vet, go test -race, UI typecheck
make build       # UI + bin/watchcat
make build-go    # binary only, no Node needed
WATCHCAT_HOME=/tmp/wd ./bin/watchcat daemon   # isolated dev instance
cd web/ui && npm ci && npm run dev            # UI hot reload on :5173, API proxied to :7878 (WATCHCAT_API overrides)
```

## Docs site

The site is MkDocs Material, built in a container from `utils/mkdocs`. Pages
live in `doc/`; the home page is generated from `README.md`. Add new pages to
`nav` in `mkdocs.yml`.

```bash
make docs-serve               # http://127.0.0.1:8000, live reload for doc/
DOCKER=podman make docs-serve # with podman instead of docker
make docs                     # strict build into site/, as CI does
```

`README.md` is copied in when the container starts, so restart
`make docs-serve` after editing it. The strict build fails on broken links
and anchors. Pushes to `main` that touch the docs publish the site to GitHub
Pages.

## Releasing

Push a `v*` tag, or run the **Release** workflow on `main` from the Actions
tab with a version such as `v0.2.0` (and optional notes). Either way it builds
the binaries and publishes the GitHub release.

## CI workflows

| Workflow | Runs on | Does |
|---|---|---|
| Build | manual | builds the binaries on Linux and macOS |
| Release | `v*` tag, or manual with a version | builds darwin/linux amd64 and arm64 binaries and publishes the GitHub release |
| Docs | push to `main` touching the docs, or manual | strict docs build, deploy to GitHub Pages |
