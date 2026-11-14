# Contributing

watchcat lives on GitHub at
[binarycat0/watchcat](https://github.com/binarycat0/watchcat). This section
covers building and testing it, the docs site and releases;
[How it works](architecture.md) explains the internals.

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
the binaries and publishes the GitHub release. The **Build** workflow builds
the binaries on demand without releasing.
