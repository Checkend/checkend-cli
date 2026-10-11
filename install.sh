#!/bin/sh
# Installs ce (the Checkend CLI) from a GitHub release, after a SHA-256 check of the download.
#
#   curl -fsSL https://raw.githubusercontent.com/Checkend/checkend-cli/main/install.sh | sh
#
# Settings: CE_VERSION (default: the latest release), CE_INSTALL_DIR (default: ~/.local/bin).
# On Windows, use `npx @checkend/cli` or the zip file of the release.
set -eu

base="${CE_DOWNLOAD_BASE:-https://github.com/Checkend/checkend-cli/releases}"
version="${CE_VERSION:-}"
dir="${CE_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

for tool in curl tar uname mkdir awk; do
	command -v "$tool" >/dev/null 2>&1 || fail "it needs $tool."
done

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "$(uname -s) is not supported. Use npx @checkend/cli, or download a release file." ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "the CPU $(uname -m) is not supported." ;;
esac

if [ -z "$version" ]; then
	# The latest release: the redirect of /latest names its tag (no API rate limit).
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$base/latest") || fail "cannot find the latest release."
	version="${latest##*/}"
fi
version="${version#v}"
case "$version" in
"" | *[!0-9A-Za-z.-]*) fail "\"$version\" is not a valid version." ;;
esac

archive="ce_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading ce $version for $os/$arch..." >&2
curl -fsSL -o "$tmp/$archive" "$base/download/v$version/$archive" || fail "cannot download $archive."
curl -fsSL -o "$tmp/checksums.txt" "$base/download/v$version/checksums.txt" || fail "cannot download checksums.txt."

expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "checksums.txt has no line for $archive."
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
else
	fail "it needs sha256sum or shasum to check the download."
fi
[ "$actual" = "$expected" ] || fail "the checksum of $archive is wrong, so nothing was installed."

tar -xzf "$tmp/$archive" -C "$tmp" ce || fail "cannot unpack $archive."
mkdir -p "$dir"
chmod 755 "$tmp/ce"
mv "$tmp/ce" "$dir/ce.new"
mv "$dir/ce.new" "$dir/ce"
ln -sf ce "$dir/checkend"

echo "Installed ce $version in $dir (also as checkend)." >&2
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to your PATH, for example: export PATH=\"$dir:\$PATH\"" >&2 ;;
esac
