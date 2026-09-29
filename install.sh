#!/bin/sh
# d9c installer for Linux and macOS.
#
# Downloads the release archive for this OS/architecture from GitHub, verifies
# its SHA-256 against the release's checksums.txt and installs the binary.
#
#   curl -fsSL https://raw.githubusercontent.com/kirg0/d9c/main/install.sh | sh
#
# Environment:
#   D9C_VERSION      release to install, e.g. v1.29.0 (default: latest)
#   D9C_INSTALL_DIR  target directory (default: /usr/local/bin — via sudo when
#                    it is not writable — otherwise ~/.local/bin)
#   D9C_BASE_URL     releases base URL (default: https://github.com/kirg0/d9c/releases)
#   D9C_OS, D9C_ARCH override platform detection (linux|darwin, amd64|arm64)
set -eu

BASE_URL="${D9C_BASE_URL:-https://github.com/kirg0/d9c/releases}"

say() { printf '%s\n' "$*"; }
die() { printf 'd9c install: %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

detect_os() {
	os="${D9C_OS:-$(uname -s)}"
	case "$os" in
	Linux | linux) echo linux ;;
	Darwin | darwin) echo darwin ;;
	*) die "unsupported OS '$os' (Windows: use Scoop or the .zip from $BASE_URL)" ;;
	esac
}

detect_arch() {
	arch="${D9C_ARCH:-$(uname -m)}"
	case "$arch" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported architecture '$arch' (prebuilt: amd64, arm64; otherwise: go install github.com/kirg0/d9c@latest)" ;;
	esac
}

# fetch URL FILE — download to a file.
fetch() {
	if has curl; then
		curl -fsSL -o "$2" "$1"
	elif has wget; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget"
	fi
}

# latest_tag — resolve the newest release tag from the /latest redirect
# (no GitHub API, so no rate limit).
latest_tag() {
	if has curl; then
		url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$BASE_URL/latest")
	elif has wget; then
		url=$(wget -S --spider --max-redirect=0 "$BASE_URL/latest" 2>&1 |
			sed -n 's/^ *[Ll]ocation: *//p' | tr -d '\r' | tail -n 1)
	else
		die "need curl or wget"
	fi
	tag="${url##*/}"
	case "$tag" in
	v[0-9]*) echo "$tag" ;;
	*) die "could not resolve the latest release (got '$url'); set D9C_VERSION" ;;
	esac
}

sha256_of() {
	if has sha256sum; then
		sha256sum "$1" | awk '{print $1}'
	elif has shasum; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif has openssl; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		die "need sha256sum, shasum or openssl to verify the download"
	fi
}

main() {
	os=$(detect_os)
	arch=$(detect_arch)
	tag="${D9C_VERSION:-}"
	if [ -z "$tag" ]; then
		tag=$(latest_tag)
	fi
	case "$tag" in v*) ;; *) tag="v$tag" ;; esac

	stage="d9c_${tag}_${os}_${arch}"
	archive="$stage.tar.gz"
	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t d9c)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading d9c $tag ($os/$arch)..."
	fetch "$BASE_URL/download/$tag/$archive" "$tmp/$archive" || die "download failed: $BASE_URL/download/$tag/$archive"
	fetch "$BASE_URL/download/$tag/checksums.txt" "$tmp/checksums.txt" || die "download failed: checksums.txt"

	want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "$archive is not listed in checksums.txt"
	got=$(sha256_of "$tmp/$archive")
	[ "$got" = "$want" ] || die "checksum mismatch for $archive (want $want, got $got)"
	say "Checksum OK"

	tar -xzf "$tmp/$archive" -C "$tmp"
	[ -f "$tmp/$stage/d9c" ] || die "archive does not contain $stage/d9c"

	dir="${D9C_INSTALL_DIR:-}"
	sudo=""
	if [ -z "$dir" ]; then
		dir=/usr/local/bin
		if [ ! -w "$dir" ]; then
			if has sudo && [ -d "$dir" ]; then
				sudo=sudo
			else
				dir="$HOME/.local/bin"
			fi
		fi
	fi
	$sudo mkdir -p "$dir"
	$sudo cp "$tmp/$stage/d9c" "$dir/d9c"
	$sudo chmod 0755 "$dir/d9c"

	say "Installed d9c $tag to $dir/d9c"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) say "Note: $dir is not on PATH — add it, e.g. export PATH=\"$dir:\$PATH\"" ;;
	esac
}

main "$@"
