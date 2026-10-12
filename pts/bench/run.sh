#!/usr/bin/env bash
# Benchmarks ParallelScript against Node, Bun and Go with hyperfine and prints a
# Markdown table for docs/BENCHMARKS.md.
#
# Usage: pts/bench/run.sh [name ...]    (default: every benchmark)
#
# A benchmark is a directory pts/bench/<name>/ with main.pts and its Go twin main.go
# (left out where Go has nothing to compare, such as async functions).
# Node and Bun run main.ts if there is one, otherwise main.pts itself when it is plain
# TypeScript; a main.pts with ParallelScript's integer types and no main.ts runs under
# ParallelScript and Go only. Every program must print the same output.
# A parallel benchmark has a file `threads` listing PTS_THREADS values ("1 2 4 8"):
# ParallelScript runs once per value, main.ts is its sequential twin for Node and Bun,
# and main.go uses every core; its table row gives the speedup over one thread (D-131).
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
  labels=()
  commands=()
  if [ -f "$dir/threads" ]; then
    for threads in $(cat "$dir/threads"); do
      labels+=("ParallelScript x$threads")
      commands+=("env PTS_THREADS=$threads $work/$name-pts")
    done
  else
    labels+=(ParallelScript)
    commands+=("$work/$name-pts")
  fi
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
  if [ -f "$dir/main.go" ]; then
    (cd "$root" && go build -o "$work/$name-go" "./pts/bench/$name")
    labels+=(Go)
    commands+=("$work/$name-go")
  fi

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

# One table row per benchmark: mean times, and how many times faster ParallelScript is;
# parallel benchmarks get a table of their own, with ParallelScript at each thread count.
python3 - "$work" "${names[@]}" <<'EOF'
import json, sys

work, names = sys.argv[1], sys.argv[2:]
results = {}
for name in names:
    with open(f"{work}/{name}.json") as f:
        results[name] = {r["command"]: r["mean"] * 1000 for r in json.load(f)["results"]}
sequential = [name for name in names if "ParallelScript" in results[name]]
parallel = [name for name in names if name not in sequential]

def cell(means, label):
    return f"{means[label]:.0f} ms" if label in means else "–"

def ratio(means, label, pts):
    return f"{means[label] / pts:.2f}×" if label in means else "–"

if sequential:
    print("| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |")
    print("|---|---|---|---|---|---|---|")
    for name in sequential:
        means = results[name]
        pts = means["ParallelScript"]
        print(f"| {name} | {pts:.0f} ms | {cell(means, 'Node')} | {cell(means, 'Bun')} | {cell(means, 'Go')} | {ratio(means, 'Node', pts)} | {ratio(means, 'Go', pts)} |")
if parallel:
    counts = sorted({int(label.split("x")[-1]) for name in parallel for label in results[name] if label.startswith("ParallelScript x")})
    most = counts[-1]
    if sequential:
        print()
    print("| Benchmark | " + " | ".join(f"PTS ×{n}" for n in counts) + f" | Speedup ×{most} | Node | Bun | Go | PTS ×{most} vs Node | PTS ×{most} vs Go |")
    print("|---|" + "---|" * (len(counts) + 6))
    for name in parallel:
        means = results[name]
        one, best = means.get("ParallelScript x1"), means.get(f"ParallelScript x{most}")
        speedup = f"{one / best:.2f}×" if one and best else "–"
        threads = " | ".join(cell(means, f"ParallelScript x{n}") for n in counts)
        print(f"| {name} | {threads} | {speedup} | {cell(means, 'Node')} | {cell(means, 'Bun')} | {cell(means, 'Go')} | {ratio(means, 'Node', best)} | {ratio(means, 'Go', best)} |")
EOF
