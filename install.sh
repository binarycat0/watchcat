#!/bin/sh
# watchcat installer.
#
#   curl -fsSL https://tools.binaryc.at/watchcat/install.sh | sh
#
# Downloads a release from GitHub, verifies its SHA-256 checksum, installs the
# binary to ~/.watchcat/bin and links it from ~/.local/bin. No sudo needed.
# Re-run it to upgrade; a running service is restarted.
#
# Environment:
#   WATCHCAT_VERSION   release tag to install, e.g. v0.1.2 (default: latest)
#   WATCHCAT_HOME      install home (default: ~/.watchcat)
#   WATCHCAT_BIN_DIR   where the `watchcat` symlink goes (default: ~/.local/bin)

set -eu

REPO="binarycat0/watchcat"

say() { printf '%s\n' "$*"; }
die() { printf 'watchcat install: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

detect_os() {
	case "$(uname -s)" in
	Darwin) echo darwin ;;
	Linux) echo linux ;;
	*) die "unsupported OS: $(uname -s) (macOS and Linux only)" ;;
	esac
}

detect_arch() {
	arch=$(uname -m)
	# A shell running under Rosetta reports x86_64 on Apple Silicon.
	if [ "$arch" = x86_64 ] && [ "$(uname -s)" = Darwin ] &&
		[ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi
	case "$arch" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) die "unsupported architecture: $arch" ;;
	esac
}

latest_version() {
	# Follow the /releases/latest redirect instead of the API (no rate limit).
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
		die "could not reach GitHub"
	tag=${url##*/}
	case "$tag" in
	v*) echo "$tag" ;;
	*) die "no release found at https://github.com/$REPO/releases" ;;
	esac
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "sha256sum or shasum is required to verify the download"
	fi
}

main() {
	need curl
	need tar
	need uname

	os=$(detect_os)
	arch=$(detect_arch)
	version=${WATCHCAT_VERSION:-$(latest_version)}
	case "$version" in v*) ;; *) version="v$version" ;; esac

	home=${WATCHCAT_HOME:-$HOME/.watchcat}
	bindir=$home/bin
	linkdir=${WATCHCAT_BIN_DIR:-$HOME/.local/bin}

	name="watchcat_${version}_${os}_${arch}.tar.gz"
	base="https://github.com/$REPO/releases/download/$version"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "downloading watchcat $version ($os/$arch)"
	curl -fsSL -o "$tmp/$name" "$base/$name" ||
		die "download failed: $base/$name"
	curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" ||
		die "download failed: $base/checksums.txt"

	want=$(awk -v f="$name" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "$name is not listed in checksums.txt"
	got=$(sha256 "$tmp/$name")
	[ "$want" = "$got" ] || die "checksum mismatch for $name (want $want, got $got)"

	tar -xzf "$tmp/$name" -C "$tmp" watchcat
	"$tmp/watchcat" version >/dev/null 2>&1 || die "downloaded binary does not run on this machine"

	# Copy, then rename over the old binary: on Apple Silicon, overwriting a
	# binary in place can get it killed by the code-signing check.
	mkdir -p "$bindir" "$linkdir"
	chmod 700 "$home"
	cp "$tmp/watchcat" "$bindir/.watchcat.new"
	chmod 755 "$bindir/.watchcat.new"
	mv -f "$bindir/.watchcat.new" "$bindir/watchcat"
	ln -sfn "$bindir/watchcat" "$linkdir/watchcat"
	say "installed $bindir/watchcat ($version), linked from $linkdir/watchcat"

	case ":$PATH:" in
	*":$linkdir:"*) ;;
	*)
		say "note: $linkdir is not on your PATH; add this to your shell profile:"
		say "  export PATH=\"$linkdir:\$PATH\""
		;;
	esac

	if "$bindir/watchcat" service status 2>/dev/null | grep -qE ': (loaded|active)'; then
		"$bindir/watchcat" service restart
	else
		say "next: watchcat init && watchcat service install && watchcat service start"
	fi
}

# Everything runs from main, so a download cut short by `curl | sh` can't run
# half a script.
main "$@"
