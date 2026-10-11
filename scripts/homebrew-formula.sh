#!/usr/bin/env bash
# Prints the Homebrew formula for a release: scripts/homebrew-formula.sh 1.2.3 dist/checksums.txt
set -euo pipefail
version="${1:?version}"
checksums="${2:?checksums.txt}"
here="$(cd "$(dirname "$0")" && pwd)"
sha() {
	local file="ce_${version}_$1.tar.gz" value
	value=$(awk -v f="$file" '$2 == f { print $1 }' "$checksums")
	[[ "$value" =~ ^[0-9a-f]{64}$ ]] || { echo "No SHA-256 for $file in $checksums." >&2; exit 1; }
	echo "$value"
}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || { echo "Bad version: $version" >&2; exit 1; }
# Each value first: a failed $(...) inside the sed arguments would not stop the script.
darwin_arm64=$(sha darwin_arm64)
darwin_amd64=$(sha darwin_amd64)
linux_arm64=$(sha linux_arm64)
linux_amd64=$(sha linux_amd64)
sed -e "s/@VERSION@/$version/g" \
	-e "s/@SHA_DARWIN_ARM64@/$darwin_arm64/" \
	-e "s/@SHA_DARWIN_AMD64@/$darwin_amd64/" \
	-e "s/@SHA_LINUX_ARM64@/$linux_arm64/" \
	-e "s/@SHA_LINUX_AMD64@/$linux_amd64/" \
	"$here/../packaging/homebrew/checkend.rb.tmpl"
