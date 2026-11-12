VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Install layout: the real binary lives in ~/.watchcat/bin next to the config
# and database; ~/.local/bin/watchcat is a symlink to it so it's on PATH.
# No sudo needed. WATCHCAT_HOME overrides ~/.watchcat.
WATCHCAT_HOME ?= $(HOME)/.watchcat
BINDIR := $(WATCHCAT_HOME)/bin
LINKDIR ?= $(HOME)/.local/bin

.PHONY: ui build build-go test install uninstall clean

# Build the React UI into web/dist/app (needs Node.js + npm).
ui:
	@command -v npm >/dev/null || { echo "npm not found: install Node.js (see README, Prerequisites)"; exit 1; }
	cd web/ui && npm ci && npm run build

# Binary with the UI embedded.
build: ui build-go

# Binary only; serves a "UI not built" page if `make ui` hasn't run.
build-go:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/watchcat ./cmd/watchcat

test:
	go vet ./...
	go test -race ./...
	@if [ -d web/ui/node_modules ]; then cd web/ui && npm run typecheck; fi

# Replace the binary atomically (copy, then rename over the old one). On
# Apple Silicon, overwriting a binary in place can get it killed by the
# code-signing check, and a running daemon keeps its old copy until restarted.
install: build
	mkdir -p $(BINDIR) $(LINKDIR)
	chmod 700 $(WATCHCAT_HOME)
	cp bin/watchcat $(BINDIR)/.watchcat.new
	chmod 755 $(BINDIR)/.watchcat.new
	mv -f $(BINDIR)/.watchcat.new $(BINDIR)/watchcat
	ln -sfn $(BINDIR)/watchcat $(LINKDIR)/watchcat
	@echo "installed $(BINDIR)/watchcat ($(VERSION)), linked from $(LINKDIR)/watchcat"
	@case ":$$PATH:" in *":$(LINKDIR):"*) ;; \
	  *) echo "note: $(LINKDIR) is not on your PATH; add this to ~/.zshrc:"; \
	     echo '  export PATH="$$HOME/.local/bin:$$PATH"';; esac
	@if $(BINDIR)/watchcat service status 2>/dev/null | grep -qE ': (loaded|active)'; then \
	  $(BINDIR)/watchcat service restart; \
	else \
	  echo "next: watchcat init && watchcat service install && watchcat service start"; \
	fi

# Removes the binary, symlink and service; keeps config and database.
uninstall:
	-$(BINDIR)/watchcat service uninstall
	rm -f $(LINKDIR)/watchcat $(BINDIR)/watchcat
	@echo "kept $(WATCHCAT_HOME) (config, database); delete it yourself if you want"

clean:
	rm -rf bin web/dist/app
