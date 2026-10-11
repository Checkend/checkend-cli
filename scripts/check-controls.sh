#!/usr/bin/env bash
# Fails when a tracked text file has a literal C1 control, bidirectional control or line/paragraph
# separator (Trojan Source). Write them as escape sequences instead.
set -euo pipefail
found=$(git ls-files -z | xargs -0 perl -CSD -ne '
  BEGIN { $| = 1 }
  next if -B $ARGV;
  print "$ARGV:$.\n" if /[\x{80}-\x{9F}\x{200E}\x{200F}\x{202A}-\x{202E}\x{2066}-\x{2069}\x{2028}\x{2029}\x{061C}]/;
  close ARGV if eof;
' 2>/dev/null || true)
if [ -n "$found" ]; then
  echo "Literal control characters (write them as escape sequences):" >&2
  echo "$found" >&2
  exit 1
fi
