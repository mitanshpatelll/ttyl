#!/bin/sh
# Installs the latest ttyl release for this Mac or Linux machine:
#   curl -fsSL https://raw.githubusercontent.com/mitanshpatelll/ttyl/main/install.sh | sh
# TTYL_VERSION=v0.9.0 picks a release, TTYL_INSTALL_DIR a folder (default: /usr/local/bin if
# writable, else ~/.local/bin). The download is checked against the release's checksums.
set -eu

repo="mitanshpatelll/ttyl"
fail() { echo "ttyl install: $*" >&2; exit 1; }

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "no build for $(uname -s); see https://github.com/$repo#install" ;;
esac
case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) fail "no build for $(uname -m)" ;;
esac

version="${TTYL_VERSION:-}"
if [ -z "$version" ]; then # the latest release, from where its page redirects
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
		fail "couldn't reach GitHub"
	version="${latest##*/}"
	case "$version" in v*) ;; *) fail "no release found" ;; esac
fi

name="ttyl_${version#v}_${os}_${arch}"
url="https://github.com/$repo/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ttyl $version for $os/$arch…"
curl -fsSL -o "$tmp/$name.tar.gz" "$url/$name.tar.gz" || fail "download failed: $url/$name.tar.gz"
curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt" || fail "couldn't get checksums.txt"

want=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
fi
[ -n "$want" ] && [ "$want" = "$got" ] || fail "checksum mismatch for $name.tar.gz, not installing"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"

dir="${TTYL_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/$name/ttyl" "$dir/ttyl"

echo "Installed $("$dir/ttyl" version) to $dir/ttyl"
case ":$PATH:" in
*":$dir:"*) echo "Run: ttyl" ;;
*) echo "Add $dir to your PATH (e.g. echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc), then run: ttyl" ;;
esac
