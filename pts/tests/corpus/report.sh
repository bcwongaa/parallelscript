#!/usr/bin/env bash
# Prints the corpus report (M6.0b5): how many programs print what Node prints (the ones
# TestCorpusMatchesNode runs), then the skipped ones by the category their `// skip:` line
# starts with: intentional differences (by design), unsupported, and failing (bug).
# Usage: pts/tests/corpus/report.sh
set -u
here=$(cd "$(dirname "$0")" && pwd)
total=$(ls "$here"/*/*.pts | wc -l)
skipped=$(grep -l "^// skip:" "$here"/*/*.pts | wc -l)
echo "Node-identical: $((total - skipped)) of $total"
for category in "by design|Intentional differences (by design)" "unsupported|Unsupported" "bug|Failing (bug)"; do
  prefix=${category%%|*}
  lines=$(grep -H -m 1 "^// skip: $prefix: " "$here"/*/*.pts | sed "s|^$here/||; s|:// skip: $prefix: |: |")
  echo
  if [ -z "$lines" ]; then
    echo "${category#*|}: 0"
    continue
  fi
  echo "${category#*|}: $(printf '%s\n' "$lines" | wc -l)"
  printf '%s\n' "$lines" | sed 's/^/  /'
done
