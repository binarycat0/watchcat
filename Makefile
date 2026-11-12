VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Install layout: the real binary lives in ~/.watchdog/bin next to the config
# and database; ~/.local/bin/watchdog is a symlink to it so it's on PATH.
# No sudo needed. WATCHDOG_HOME overrides ~/.watchdog.
WATCHDOG_HOME ?= $(HOME)/.watchdog
BINDIR := $(WATCHDOG_HOME)/bin
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
	go build -ldflags "-X main.version=$(VERSION)" -o bin/watchdog ./cmd/watchdog

test:
	go vet ./...
	go test -race ./...
	@if [ -d web/ui/node_modules ]; then cd web/ui && npm run typecheck; fi

# Replace the binary atomically (copy, then rename over the old one). On
# Apple Silicon, overwriting a binary in place can get it killed by the
# code-signing check, and a running daemon keeps its old copy until restarted.
install: build
	mkdir -p $(BINDIR) $(LINKDIR)
	chmod 700 $(WATCHDOG_HOME)
	cp bin/watchdog $(BINDIR)/.watchdog.new
	chmod 755 $(BINDIR)/.watchdog.new
	mv -f $(BINDIR)/.watchdog.new $(BINDIR)/watchdog
	ln -sfn $(BINDIR)/watchdog $(LINKDIR)/watchdog
	@echo "installed $(BINDIR)/watchdog ($(VERSION)), linked from $(LINKDIR)/watchdog"
	@case ":$$PATH:" in *":$(LINKDIR):"*) ;; \
	  *) echo "note: $(LINKDIR) is not on your PATH; add this to ~/.zshrc:"; \
	     echo '  export PATH="$$HOME/.local/bin:$$PATH"';; esac
	@if $(BINDIR)/watchdog service status 2>/dev/null | grep -qE ': (loaded|active)'; then \
	  $(BINDIR)/watchdog service restart; \
	else \
	  echo "next: watchdog init && watchdog service install && watchdog service start"; \
	fi

# Removes the binary, symlink and service; keeps config and database.
uninstall:
	-$(BINDIR)/watchdog service uninstall
	rm -f $(LINKDIR)/watchdog $(BINDIR)/watchdog
	@echo "kept $(WATCHDOG_HOME) (config, database); delete it yourself if you want"

clean:
	rm -rf bin web/dist/app
