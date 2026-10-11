#!/usr/bin/env bash
# Benchmarks ParallelScript against Node, Bun and Go with hyperfine and prints a
# Markdown table for docs/BENCHMARKS.md.
#
# Usage: pts/bench/run.sh [name ...]    (default: every benchmark)
#
# A benchmark is a directory pts/bench/<name>/ with main.pts and its Go twin main.go.
# Node and Bun run main.ts if there is one, otherwise main.pts itself when it is plain
# TypeScript; a main.pts with ParallelScript's integer types and no main.ts runs under
# ParallelScript and Go only. Every program must print the same output.
# Environment: RUNS (default 10) and WARMUP (default 2) are passed to hyperfine.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
bench=$root/pts/bench
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for tool in hyperfine go node bun clang python3; do
  command -v "$tool" >/dev/null || { echo "run.sh: $tool is not on PATH" >&2; exit 1; }
done

if [ $# -gt 0 ]; then
  names=("$@")
else
  names=()
  for dir in "$bench"/*/; do names+=("$(basename "$dir")"); done
fi

echo "Building pts..." >&2
go build -o "$work/pts" "$root/cmd/pts"
export PTS_HOME=$root

# ptsOnly reports whether a .pts file uses ParallelScript's integer types, which Node and Bun don't know.
ptsOnly() {
  grep -Eq '(:|<|,|\||\bas)\s*(int|i8|i16|i32|i64|u8|u16|u32|u64|f32|f64)\b' "$1"
}

for name in "${names[@]}"; do
  dir=$bench/$name
  echo "== $name" >&2
  "$work/pts" build "$dir/main.pts" -o "$work/$name-pts"
  (cd "$root" && go build -o "$work/$name-go" "./pts/bench/$name")
  labels=(ParallelScript)
  commands=("$work/$name-pts")
  script=""
  if [ -f "$dir/main.ts" ]; then
    script=$dir/main.ts
  elif ! ptsOnly "$dir/main.pts"; then
    script=$work/$name.mts
    cp "$dir/main.pts" "$script"
  fi
  if [ -n "$script" ]; then
    labels+=(Node Bun)
    commands+=("node --no-warnings $script" "bun $script")
  fi
  labels+=(Go)
  commands+=("$work/$name-go")

  # Every program must compute the same thing.
  expected=$(${commands[0]})
  for i in "${!commands[@]}"; do
    actual=$(${commands[$i]})
    if [ "$actual" != "$expected" ]; then
      echo "run.sh: $name: ${labels[$i]} prints different output:" >&2
      diff <(echo "$expected") <(echo "$actual") >&2 || true
      exit 1
    fi
  done

  args=()
  for i in "${!commands[@]}"; do args+=(-n "${labels[$i]}" "${commands[$i]}"); done
  hyperfine --shell=none --warmup "${WARMUP:-2}" --runs "${RUNS:-10}" --export-json "$work/$name.json" "${args[@]}" >&2
done

# One table row per benchmark: mean times, and how many times faster ParallelScript is.
python3 - "$work" "${names[@]}" <<'EOF'
import json, sys

work, names = sys.argv[1], sys.argv[2:]
print("| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |")
print("|---|---|---|---|---|---|---|")
for name in names:
    with open(f"{work}/{name}.json") as f:
        means = {r["command"]: r["mean"] * 1000 for r in json.load(f)["results"]}
    pts = means["ParallelScript"]
    cell = lambda label: f"{means[label]:.0f} ms" if label in means else "–"
    ratio = lambda label: f"{means[label] / pts:.2f}×" if label in means else "–"
    print(f"| {name} | {pts:.0f} ms | {cell('Node')} | {cell('Bun')} | {cell('Go')} | {ratio('Node')} | {ratio('Go')} |")
EOF
