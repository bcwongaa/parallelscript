#!/usr/bin/env bash
# Removes the `// skip:` first line of each skipped corpus program that now prints what
# Node prints (compare.sh on a copy without it), and lists them.
# Usage (after `make build`): pts/tests/corpus/unskip.sh [file.pts…] (default: every skipped program)
set -u
here=$(cd "$(dirname "$0")" && pwd)
files=("$@")
if [ ${#files[@]} -eq 0 ]; then
  mapfile -t files < <(grep -l "^// skip:" "$here"/*/*.pts)
fi
for f in "${files[@]}"; do
  head -n 1 "$f" | grep -q "^// skip:" || continue
  tmp=$(mktemp -d)
  copy="$tmp/$(basename "$f")"
  tail -n +2 "$f" >"$copy"
  if "$here/compare.sh" "$copy" 2>/dev/null | grep -q "^PASS"; then
    cp "$copy" "$f"
    echo "unskipped $f"
  fi
  rm -rf "$tmp"
done
