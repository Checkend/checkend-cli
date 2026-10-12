#!/bin/sh
# Installs ce (the Checkend CLI) from a GitHub release. It checks the SSH signature of
# checksums.txt (ssh-keygen -Y verify, a trusted key below) and then the SHA-256 of the download.
# Without a valid signature by a trusted key, it installs nothing.
#
#   curl -fsSL https://raw.githubusercontent.com/Checkend/checkend-cli/main/install.sh | sh
#
# Settings: CE_VERSION (default: the latest release), CE_INSTALL_DIR (default: ~/.local/bin).
# On Windows, use `npx @checkend/cli` or the zip file of the release.
set -eu

base="${CE_DOWNLOAD_BASE:-https://github.com/Checkend/checkend-cli/releases}"
version="${CE_VERSION:-}"
dir="${CE_INSTALL_DIR:-$HOME/.local/bin}"

# The keys that sign CLI releases, in the allowed_signers format of ssh-keygen. A new key is added
# here before its first release; an old key stays until no supported release uses it. The
# server releases use another key.
allowed_signers=$(cat <<'EOF'
# BEGIN ALLOWED SIGNERS
# cli-2026: SHA256:yoR4iXfhZdJ4v5OaLXX+5/rupH9z93AhZGdas6/AP88
cli-2026 namespaces="checkend-cli-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAION9vmE2Vp0SRkz+rdN0fnC9IvpPP5chwaCbNOLXlEI/
# END ALLOWED SIGNERS
EOF
)
namespace=checkend-cli-release

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

for tool in curl tar uname mkdir awk; do
	command -v "$tool" >/dev/null 2>&1 || fail "it needs $tool."
done
command -v ssh-keygen >/dev/null 2>&1 ||
	fail "it needs ssh-keygen (OpenSSH 8.1 or later) to check the signature of the release. Install the OpenSSH client, then run it again."
case "$(ssh-keygen -Y verify 2>&1)" in
*"option -- Y"*) fail "ssh-keygen is too old to check signatures: it needs OpenSSH 8.1 or later." ;;
esac
signers=$(printf '%s\n' "$allowed_signers" | awk '$1 != "" && $1 !~ /^#/ { print $1 }')
[ -n "$signers" ] ||
	fail "this install.sh trusts no release key yet, so it installs nothing. Use: go install github.com/Checkend/checkend-cli/cmd/ce@latest"

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
curl -fsSL -o "$tmp/checksums.txt.sig" "$base/download/v$version/checksums.txt.sig" ||
	fail "the release v$version has no signature (checksums.txt.sig), so nothing was installed."

# The signature first: only a signed checksums.txt says which archive is the real one.
printf '%s\n' "$allowed_signers" >"$tmp/allowed_signers"
signer=
for principal in $signers; do
	if ssh-keygen -Y verify -f "$tmp/allowed_signers" -I "$principal" -n "$namespace" \
		-s "$tmp/checksums.txt.sig" <"$tmp/checksums.txt" >/dev/null 2>&1; then
		signer=$principal
		break
	fi
done
[ -n "$signer" ] ||
	fail "the signature of checksums.txt is not valid or not made by a trusted key, so nothing was installed."
echo "checksums.txt is signed by $signer." >&2

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
