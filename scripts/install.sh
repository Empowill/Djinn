#!/bin/sh
# Install Djinn on macOS or Linux, without sudo: the binary of the latest release for this system, checked against
# the release's SHA-256 sums, in ~/.local/bin. On Linux without WebKitGTK it takes the build without a window, which
# opens Djinn in your browser. When no binary fits, it falls back on `go install` without CGO: the browser too.
#
#   curl -fsSL https://github.com/Empowill/Djinn/releases/latest/download/install.sh | sh
#
# Settings, all optional, as environment variables:
#   DJINN_VERSION      a release tag, such as v0.1.0; the latest release by default
#   DJINN_INSTALL_DIR  where djinn goes; ~/.local/bin by default
#   DJINN_ASSET        the archive to take, without .tar.gz (djinn_linux_amd64_gtk4, djinn_linux_amd64_browser); picked
#                      for this system by default
#   DJINN_RELEASES     where releases come from; https://github.com/Empowill/Djinn/releases by default. With curl, a
#                      local folder too (file:///path), laid out as GitHub serves a release: latest/download/<file>
#                      or download/<tag>/<file>, to try a release built by hand before it is published
set -eu

releases=${DJINN_RELEASES:-https://github.com/Empowill/Djinn/releases}
releases=${releases%/}
version=${DJINN_VERSION:-latest}
dir=${DJINN_INSTALL_DIR:-$HOME/.local/bin}
module=github.com/empowill/djinn/cmd/djinn

if [ "$version" = latest ]; then
	base=$releases/latest/download
else
	base=$releases/download/$version
fi

say() { printf 'djinn: %s\n' "$*" >&2; }
die() {
	say "$*"
	exit 1
}

fetch() { # <url> <file>
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "this script needs curl or wget"
	fi
}

sha256() { # <file>: its SHA-256, in hexadecimal
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 -r "$1" | cut -d ' ' -f 1
	else
		die "this script needs sha256sum, shasum or openssl to check the download"
	fi
}

# The archives that may fit this machine, best first. On Linux the window needs the WebKitGTK of its build; the
# browser build needs nothing, and comes last.
candidates() {
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) return 0 ;;
	esac
	case $(uname -s) in
	Darwin) echo djinn_darwin_universal ;;
	Linux)
		libs=$({ /sbin/ldconfig -p || ldconfig -p; } 2>/dev/null || true)
		if [ -z "$libs" ]; then
			# No library cache to read: try both, and let the binary tell whether it starts.
			echo "djinn_linux_$arch djinn_linux_${arch}_gtk4 djinn_linux_${arch}_browser"
			return 0
		fi
		found=""
		case $libs in *libwebkit2gtk-4.1.so*) found="djinn_linux_$arch" ;; esac
		case $libs in *libwebkitgtk-6.0.so*) found="$found djinn_linux_${arch}_gtk4" ;; esac
		echo "$found djinn_linux_${arch}_browser"
		;;
	esac
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# try <asset>: download it, check it, and check that it starts here; on success $bin is the binary to install.
# Fails when it does not fit; a download that does not match its sum stops everything.
try() {
	archive=$1.tar.gz
	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/SHA256SUMS")
	[ -n "$want" ] || return 1
	say "downloading $archive"
	fetch "$base/$archive" "$tmp/$archive" || return 1
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || die "$archive does not match its SHA-256 sum: nothing was installed. Do not use this download."
	tar -xzf "$tmp/$archive" -C "$tmp" || return 1
	bin=$tmp/$1/djinn
	# DJINN_HOME keeps this check away from any Djinn data.
	if ! DJINN_HOME="$tmp/home" "$bin" version >/dev/null 2>&1; then
		say "$1 does not start on this system (a missing library?)"
		return 1
	fi
}

with_go() {
	say "$1"
	command -v go >/dev/null 2>&1 ||
		die "and Go is not installed. Install Go (https://go.dev/dl/), then run this script again."
	say "installing with go install, without CGO: djinn up will open in your browser"
	GOBIN=$dir CGO_ENABLED=0 go install "$module@$version"
}

mkdir -p "$dir"
dir=$(cd "$dir" && pwd) # go install wants an absolute GOBIN.

installed=""
if fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
	for asset in ${DJINN_ASSET:-$(candidates)}; do
		if try "$asset"; then
			installed=$asset
			break
		fi
	done
	if [ -z "$installed" ]; then
		reason="no binary of this release fits $(uname -s) $(uname -m)"
		[ "$(uname -s)" = Linux ] && reason="$reason with its WebKitGTK (libwebkit2gtk-4.1 or libwebkitgtk-6.0)"
		with_go "$reason"
	else
		# Copied beside, then renamed over: a Djinn that runs keeps its file, and offers the new one.
		cp "$bin" "$dir/.djinn-new"
		chmod 755 "$dir/.djinn-new"
		mv -f "$dir/.djinn-new" "$dir/djinn"
		case $installed in *_browser)
			say "no WebKitGTK here: djinn up will open in your browser."
			say "for its own window, install libwebkit2gtk-4.1 (or libwebkitgtk-6.0), then run this script again."
			;;
		esac
	fi
else
	with_go "no release found at $base"
fi

say "installed $(DJINN_HOME="$tmp/home" "$dir/djinn" version 2>/dev/null || echo djinn) in $dir"
case ":$PATH:" in
*":$dir:"*) say "start it with: djinn up" ;;
*) say "add $dir to your PATH, then start it with: djinn up" ;;
esac
