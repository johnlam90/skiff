#!/bin/sh
# =============================================================================
# File: install.sh
# Author: Spicer Matthews <spicer@cloudmanic.com>
# Created: 2026-04-30
# Copyright: 2026 Cloudmanic, LLC. All rights reserved.
# =============================================================================
#
# One-liner installer / upgrader for Skiff. Detects the host OS and
# architecture, downloads the matching archive from the latest GitHub
# Release, verifies it against the release's published checksums.txt,
# and, when cosign is installed, verifies the release signature.
# Extracts the static `skiff` binary and drops it into ~/.local/bin
# (preferred) or /usr/local/bin. Re-running the script performs an
# upgrade — same flow, latest version. A missing checksum entry, a
# mismatch, or a host with no sha256 tool aborts the install; there is
# deliberately no "skip verification" escape hatch.
#
# Usage:
#
#   curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh | sh
#
# Override the install location:
#
#   curl -fsSL .../install.sh | INSTALL_DIR=/opt/bin sh
#
# Override the version (default is the latest GitHub Release):
#
#   curl -fsSL .../install.sh | VERSION=v0.0.13 sh
#
# Designed to run under POSIX `sh` so it works on Alpine / BusyBox /
# minimal SSH targets in addition to bash on a normal Linux box.

set -eu

REPO="johnlam90/skiff"
BINARY="skiff"

# Pretty output when stderr is a terminal, plain otherwise so piping into
# logs doesn't end up with stray escape codes.
if [ -t 2 ]; then
	BOLD="$(printf '\033[1m')"
	DIM="$(printf '\033[2m')"
	GREEN="$(printf '\033[32m')"
	RED="$(printf '\033[31m')"
	YELLOW="$(printf '\033[33m')"
	RESET="$(printf '\033[0m')"
else
	BOLD=""
	DIM=""
	GREEN=""
	RED=""
	YELLOW=""
	RESET=""
fi

# info prints a step-prefixed line to stderr so it doesn't pollute any
# stdout the user might be capturing.
info() {
	printf '%s==>%s %s\n' "$GREEN" "$RESET" "$1" >&2
}

warn() {
	printf '%s==>%s %s\n' "$YELLOW" "$RESET" "$1" >&2
}

# fatal prints an error and exits non-zero. Used everywhere a precondition
# fails — the script should never silently fall through into a bad state.
fatal() {
	printf '%serror:%s %s\n' "$RED" "$RESET" "$1" >&2
	exit 1
}

# detect_os normalises uname -s into the lowercase token GoReleaser uses
# in archive names (linux, darwin). Anything else is an explicit error
# rather than a guess — we'd rather tell the user "not supported" than
# 404 on the download.
detect_os() {
	uname_s="$(uname -s 2>/dev/null || echo unknown)"
	case "$uname_s" in
		Linux) echo "linux" ;;
		Darwin) echo "darwin" ;;
		*) fatal "unsupported OS: $uname_s (only Linux and macOS are released)" ;;
	esac
}

# detect_arch maps uname -m onto GoReleaser's arch token (amd64 or arm64).
# x86_64 / amd64 are aliases; aarch64 / arm64 are aliases. Anything else
# (e.g. armv7, riscv64) is an explicit error — we don't ship those builds.
detect_arch() {
	uname_m="$(uname -m 2>/dev/null || echo unknown)"
	case "$uname_m" in
		x86_64|amd64) echo "amd64" ;;
		aarch64|arm64) echo "arm64" ;;
		*) fatal "unsupported architecture: $uname_m (only amd64 and arm64 are released)" ;;
	esac
}

# require_cmd ensures cmd is on PATH. We use it to assert tar plus one of
# curl / wget exists before doing anything else, so failures show up at
# the top of the run rather than after a half-finished download.
require_cmd() {
	command -v "$1" >/dev/null 2>&1 || fatal "$1 is required but not found on PATH"
}

# fetch downloads url to outfile using curl or wget (whichever is present).
# We try curl first because it's near-universal and gives clean redirect
# behaviour with -L; wget is the BusyBox / minimal-image fallback — and
# BusyBox wget only knows the short options, so the long spellings GNU
# wget accepts (--quiet, --output-document) are an "unrecognized option"
# there. -q and -O mean the same thing to both.
fetch() {
	url="$1"
	outfile="$2"
	if command -v curl >/dev/null 2>&1; then
		curl --fail --show-error --silent --location --output "$outfile" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$outfile" "$url"
	else
		fatal "need curl or wget to download release archives"
	fi
}

# sha256_of prints the lowercase sha256 of a file using whichever hasher
# the host ships: Linux and BusyBox have `sha256sum`, macOS has `shasum`
# instead. Returns non-zero when neither exists so the caller can abort —
# "no hasher, install anyway" is not a tradeoff this script gets to make
# on the user's behalf.
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1 | tr 'A-F' 'a-f'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1 | tr 'A-F' 'a-f'
	else
		return 1
	fi
}

# verify_checksum aborts unless the downloaded archive's sha256 matches the
# entry GoReleaser published in checksums.txt for this tag. This is remote
# code about to land on the user's PATH, so a truncated CDN response, a
# stale mirror, or a swapped asset has to be a loud failure — never a
# warning the curl-pipe-sh user will scroll straight past.
#
# checksums.txt is `<sha256>␠␠<filename>`; awk field-matches the exact name
# (with an optional binary-mode `*` prefix) so a `.` in the archive name
# can't act as a regex wildcard and pick up a neighbouring line.
verify_checksum() {
	archive_path="$1"
	archive_name="$2"
	sums_file="$3"

	expected="$(awk -v want="$archive_name" \
		'$2 == want || $2 == "*" want { print tolower($1); exit }' "$sums_file")"
	if [ -z "$expected" ]; then
		fatal "checksums.txt has no entry for $archive_name — refusing to install unverified bytes"
	fi

	actual="$(sha256_of "$archive_path")" \
		|| fatal "no sha256 tool on PATH (need sha256sum or shasum) — refusing to install unverified bytes"

	if [ "$actual" != "$expected" ]; then
		printf '%serror:%s checksum mismatch for %s\n' "$RED" "$RESET" "$archive_name" >&2
		printf '  expected %s\n' "$expected" >&2
		printf '  actual   %s\n' "$actual" >&2
		fatal "the download does not match the published checksum — refusing to install (corrupt or incomplete download)"
	fi
}

# verify_signature checks checksums.txt's cosign signature when cosign is
# on PATH. verify_checksum already proves the archive matches what this
# release published; this proves the release itself was published by this
# repo's own release workflow (keyless OIDC signing), not a swapped
# publishing path. It is the ceiling, not the floor: cosign missing, or a
# release published before signing landed (no .cosign.bundle asset), is
# reported and the install continues on the checksum alone.
#
# cosign v4 defaults to the bundle format (one file carries both the
# signature and the certificate) rather than separate .sig/.pem assets —
# .goreleaser.yml's signs: block publishes checksums.txt.cosign.bundle
# to match.
verify_signature() {
	sums_file="$1"
	bundle_url="$2"
	tmp_dir="$3"

	if ! command -v cosign >/dev/null 2>&1; then
		info "  cosign not found — skipping signature check (checksum already verified)"
		return
	fi

	if ! fetch "$bundle_url" "$tmp_dir/checksums.txt.cosign.bundle" 2>/dev/null; then
		info "  no signature published for ${version} — skipping signature check (checksum already verified)"
		return
	fi

	# The identity is anchored at both ends and pinned to main. cosign
	# treats this as an unanchored regular expression, so the old
	# "https://github.com/<repo>/…/release.yml@.*" also matched any URL
	# that merely CONTAINED that text, and any ref of the workflow — a
	# release signed from a throwaway branch verified as well as one
	# from main. Every real release is cut from main (see release.yml).
	#
	# cosign's own stderr is kept and shown on failure: "refusing to
	# install" with the reason thrown away leaves the user unable to
	# tell an expired certificate from a tampered file.
	identity="^https://github\\.com/${REPO}/\\.github/workflows/release\\.yml@refs/heads/main\$"
	if ! cosign verify-blob \
		--bundle "$tmp_dir/checksums.txt.cosign.bundle" \
		--certificate-identity-regexp "$identity" \
		--certificate-oidc-issuer https://token.actions.githubusercontent.com \
		"$sums_file" >/dev/null 2>"$tmp_dir/cosign.err"; then
		sed 's/^/  cosign: /' "$tmp_dir/cosign.err" >&2
		fatal "checksums.txt signature verification failed — refusing to install"
	fi
	info "  signature verified"
}

# normalize_tag turns a version the user or GitHub handed us into the
# tag it names, or prints nothing if it is not one. Tags are "v" plus a
# dotted version; VERSION=0.2.27 is accepted and gains the "v", because
# every other place a version is written (the README, `skiff --version`)
# shows it bare. Anything with a character that does not belong in a tag
# is refused rather than pasted into a download URL.
normalize_tag() {
	tag="$1"
	case "$tag" in
		v*) ;;
		*) tag="v$tag" ;;
	esac
	case "$tag" in
		v[0-9]*) ;;
		*) return 1 ;;
	esac
	case "$tag" in
		*[!A-Za-z0-9._-]*) return 1 ;;
	esac
	printf '%s\n' "$tag"
}

# tag_from_headers reads HTTP response headers on stdin and prints the
# release tag named by the last Location: header that points at one
# (…/releases/tag/v0.0.13 -> v0.0.13). Everything after the tag is
# dropped: the carriage return every header line ends in, and the
# " [following]" GNU wget appends when it echoes a redirect — which the
# old "take everything after the last slash" kept as part of the tag.
tag_from_headers() {
	sed -n 's#^[[:space:]]*[Ll]ocation:.*/releases/tag/\([A-Za-z0-9._-]*\).*#\1#p' | tail -n 1
}

# resolve_version picks the version to install. If the user passed VERSION,
# it is used (allowing pinned installs), with or without its leading "v".
# Otherwise we follow GitHub's /releases/latest redirect — that's a
# single HTTP call with no API rate limit, no JSON parsing, and works
# behind corporate caches.
resolve_version() {
	if [ -n "${VERSION:-}" ]; then
		normalize_tag "$VERSION" \
			|| fatal "VERSION=$VERSION is not a release tag (expected something like v0.2.27)"
		return
	fi
	url="https://github.com/${REPO}/releases/latest"
	# Headers only. curl -I follows the redirect and prints each hop's
	# headers. wget has no HEAD mode that BusyBox shares, so it fetches
	# the page into /dev/null and -S prints the headers of every hop to
	# stderr; -S, -q and -O are the options BusyBox and GNU wget have in
	# common (--max-redirect and --server-response are GNU-only).
	if command -v curl >/dev/null 2>&1; then
		found="$(curl --silent --location --head "$url" 2>/dev/null | tag_from_headers)"
	elif command -v wget >/dev/null 2>&1; then
		found="$(wget -S -q -O /dev/null "$url" 2>&1 | tag_from_headers)"
	else
		fatal "need curl or wget to find the latest release"
	fi
	normalize_tag "${found:-}" \
		|| fatal "could not resolve latest version from $url"
}

# pick_install_dir chooses where to drop the binary, honoring INSTALL_DIR
# when set. Default preference: ~/.local/bin (no sudo, in PATH on most
# modern distros), falling back to /usr/local/bin (needs sudo, in PATH
# everywhere).
pick_install_dir() {
	if [ -n "${INSTALL_DIR:-}" ]; then
		echo "$INSTALL_DIR"
		return
	fi
	# HOME can be unset (a bare `env -i`, some init contexts) and this
	# script runs under `set -u`; without one there is no per-user bin,
	# and "/.local/bin" is not a place to install to.
	home="${HOME:-}"
	if [ -n "$home" ] && { [ -d "$home/.local/bin" ] || mkdir -p "$home/.local/bin" 2>/dev/null; }; then
		echo "$home/.local/bin"
		return
	fi
	echo "/usr/local/bin"
}

# install_binary puts the extracted binary into the chosen directory.
# When the directory isn't writable, the same steps run under sudo if
# it's available — most "system" install dirs need root but we don't
# want to silently fail with a permission error.
#
# The binary is COPIED to a temporary name beside its destination, made
# executable, and only then renamed into place:
#
#   - A rename within one directory is atomic and works while the old
#     binary is running. Moving straight from /tmp is a copy when /tmp
#     is another filesystem, and writing over a running executable
#     fails with "text file busy" — which is every upgrade run from
#     inside a skiff session.
#   - A copy made under sudo belongs to root. `sudo mv` of the file we
#     extracted kept OUR ownership on a binary in a root-owned
#     directory, leaving it writable by the unprivileged user.
#   - A failure part way leaves the old binary untouched.
install_binary() {
	src="$1"
	dest_dir="$2"
	dest="$dest_dir/$BINARY"
	stage="$dest_dir/.$BINARY.new.$$"

	if [ -w "$dest_dir" ] || { [ ! -e "$dest_dir" ] && mkdir -p "$dest_dir" 2>/dev/null; }; then
		if cp "$src" "$stage" && chmod 0755 "$stage" && mv -f "$stage" "$dest"; then
			return
		fi
		rm -f "$stage"
		fatal "could not install to $dest"
	fi
	if command -v sudo >/dev/null 2>&1; then
		warn "writing to $dest_dir requires sudo"
		sudo mkdir -p "$dest_dir"
		if sudo cp "$src" "$stage" && sudo chmod 0755 "$stage" && sudo mv -f "$stage" "$dest"; then
			return
		fi
		sudo rm -f "$stage"
		fatal "could not install to $dest"
	fi
	fatal "cannot write to $dest_dir and sudo is not available"
}

# warn_if_not_in_path nudges the user to fix their PATH if we just
# installed somewhere they can't run from. The most common case is a
# fresh ~/.local/bin on an older shell rc.
warn_if_not_in_path() {
	dir="$1"
	case ":${PATH:-}:" in
		*":$dir:"*) return ;;
	esac
	warn "$dir is not on your \$PATH — add this to your shell rc:"
	# shellcheck disable=SC2016  # $PATH is meant to stay literal — this is
	# a line the user copies into their shell rc, not one we expand here.
	printf '\n    %sexport PATH="%s:\$PATH"%s\n\n' "$BOLD" "$dir" "$RESET" >&2
}

main() {
	require_cmd tar
	require_cmd awk

	os="$(detect_os)"
	arch="$(detect_arch)"
	version="$(resolve_version)"
	# resolve_version always returns the tag, "v" included, whichever
	# way VERSION was spelled. Strip it so the version slot in the
	# archive name matches GoReleaser's {{ .Version }} (which is bare).
	bare_version="${version#v}"

	archive="${BINARY}_${bare_version}_${os}_${arch}.tar.gz"
	url="https://github.com/${REPO}/releases/download/${version}/${archive}"
	sums_url="https://github.com/${REPO}/releases/download/${version}/checksums.txt"

	info "Installing ${BINARY} ${version} (${os}/${arch})"
	info "  source: ${url}"

	tmp="$(mktemp -d)"
	# Clean up on any exit, including failure paths — we don't want to
	# leave half-extracted archives in /tmp littering the filesystem.
	# A signal also has to END the script: a trap that only cleans up
	# returns to where it was interrupted, so Ctrl-C deleted the temp
	# directory and the install carried on without it. 130 and 143 are
	# the conventional statuses for SIGINT and SIGTERM.
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM

	fetch "$url" "$tmp/$archive" \
		|| fatal "download failed (was the release published with this archive name?)"

	# Verify BEFORE extracting: tar on a hostile archive is already too
	# much trust to hand a file we haven't authenticated.
	fetch "$sums_url" "$tmp/checksums.txt" \
		|| fatal "could not download checksums.txt for ${version} — refusing to install unverified bytes"
	verify_checksum "$tmp/$archive" "$archive" "$tmp/checksums.txt"
	info "  sha256 verified"
	verify_signature "$tmp/checksums.txt" \
		"https://github.com/${REPO}/releases/download/${version}/checksums.txt.cosign.bundle" \
		"$tmp"

	tar -xzf "$tmp/$archive" -C "$tmp" \
		|| fatal "extraction failed (archive may be corrupt)"

	if [ ! -f "$tmp/$BINARY" ]; then
		fatal "archive did not contain a $BINARY binary"
	fi

	dest_dir="$(pick_install_dir)"
	info "Installing to ${dest_dir}/${BINARY}"
	install_binary "$tmp/$BINARY" "$dest_dir"

	info "Done. ${BOLD}${dest_dir}/${BINARY}${RESET}${DIM} (${version})${RESET}"
	warn_if_not_in_path "$dest_dir"
}

main "$@"
