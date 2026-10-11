#!/usr/bin/env bash
# Runs programs under Node and pts and says whether their stdout and exit codes match,
# as TestCorpusMatchesNode does, with the first differing lines when they don't.
# Usage (from anywhere, after `make build`): pts/tests/corpus/compare.sh file.pts...
set -u
root=$(cd "$(dirname "$0")/../../.." && pwd)
pts="${PTS:-$root/pts-bin/pts}" # PTS overrides the compiler
export TZ=America/New_York # the tests' time zone (D-087)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for file in "$@"; do
  name=$(basename "$file" .pts)
  cp "$file" "$tmp/$name.mts"
  (cd "$tmp" && node --no-warnings --experimental-transform-types "$name.mts" >node.out 2>node.err)
  node_code=$?
  if ! "$pts" build "$file" -o "$tmp/$name" 2>"$tmp/build.err" >&2; then
    echo "FAIL $file: compile error"
    head -n 5 "$tmp/build.err"
    continue
  fi
  (cd "$tmp" && "./$name" >pts.out 2>pts.err)
  pts_code=$?
  if cmp -s "$tmp/node.out" "$tmp/pts.out" && [ "$node_code" = "$pts_code" ]; then
    echo "PASS $file"
  else
    echo "FAIL $file: output differs (Node exit $node_code, pts exit $pts_code)"
    diff "$tmp/node.out" "$tmp/pts.out" | head -n 10
    head -n 3 "$tmp/pts.err"
  fi
done
