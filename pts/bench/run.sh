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
# Server benchmarks (a directory with a `request` file) are serve.sh's, and run.sh skips them.
# A file `label` in a benchmark's directory is a note shown after its name in the table
# ("warm cache" for one that reads files it wrote before, which the page cache holds).
#
# Every time is a whole process's wall-clock time, its start included (the `startup`
# benchmark shows what starting costs each implementation). The runs of a benchmark go
# in ROUNDS rounds, each running every implementation in turn, so that a change in the
# machine's load hits them all alike.
# Environment: RUNS (default 10) runs of each implementation in all, split over ROUNDS
# (default 2) rounds, each with WARMUP (default 2) warm-up runs; RESULTS (default
# pts/bench/results/latest) keeps hyperfine's JSON of every round and <name>.json, every
# run's time merged, from which the table's means and standard deviations come.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
bench=$root/pts/bench
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
results=${RESULTS:-$bench/results/latest}
rounds=${ROUNDS:-2}
runs=$(( (${RUNS:-10} + rounds - 1) / rounds ))
mkdir -p "$results"

for tool in hyperfine go node bun clang python3; do
  command -v "$tool" >/dev/null || { echo "run.sh: $tool is not on PATH" >&2; exit 1; }
done

if [ $# -gt 0 ]; then
  names=("$@")
else
  names=()
  for dir in "$bench"/*/; do
    if [ ! -f "$dir/request" ]; then names+=("$(basename "$dir")"); fi
  done
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
  rm -f "$results/$name".round*.json
  for round in $(seq "$rounds"); do
    hyperfine --shell=none --warmup "${WARMUP:-2}" --runs "$runs" --export-json "$results/$name.round$round.json" "${args[@]}" >&2
  done
  if [ -f "$dir/label" ]; then cp "$dir/label" "$results/$name.label"; else rm -f "$results/$name.label"; fi
done

# Each benchmark's rounds merged into <name>.json, then one table row per benchmark:
# mean ± standard deviation, and how many times faster ParallelScript is; parallel
# benchmarks get a table of their own, with ParallelScript at each thread count.
python3 - "$results" "${names[@]}" <<'EOF'
import glob, json, os, statistics, sys

directory, names = sys.argv[1], sys.argv[2:]
results, spreads, titles = {}, {}, {}
for name in names:
    times = {}
    for path in sorted(glob.glob(f"{directory}/{name}.round*.json")):
        with open(path) as f:
            for result in json.load(f)["results"]:
                times.setdefault(result["command"], []).extend(result["times"])
    merged = [{"command": label, "times": ts, "mean": statistics.mean(ts), "stddev": statistics.stdev(ts) if len(ts) > 1 else 0.0,
               "min": min(ts), "max": max(ts)} for label, ts in times.items()]
    with open(f"{directory}/{name}.json", "w") as f:
        json.dump({"results": merged}, f, indent=1)
    results[name] = {r["command"]: r["mean"] * 1000 for r in merged}
    spreads[name] = {r["command"]: r["stddev"] * 1000 for r in merged}
    titles[name] = name
    if os.path.exists(f"{directory}/{name}.label"):
        with open(f"{directory}/{name}.label") as f:
            titles[name] = f"{name} ({f.read().strip()})"
sequential = [name for name in names if "ParallelScript" in results[name]]
parallel = [name for name in names if name not in sequential]

def cell(name, label):
    means = results[name]
    if label not in means:
        return "–"
    digits = 1 if means[label] < 10 else 0
    return f"{means[label]:.{digits}f} ± {spreads[name][label]:.{digits}f} ms"

def ratio(means, label, pts):
    return f"{means[label] / pts:.2f}×" if label in means else "–"

if sequential:
    print("| Benchmark | ParallelScript | Node | Bun | Go | PTS vs Node | PTS vs Go |")
    print("|---|---|---|---|---|---|---|")
    for name in sequential:
        means = results[name]
        pts = means["ParallelScript"]
        print(f"| {titles[name]} | {cell(name, 'ParallelScript')} | {cell(name, 'Node')} | {cell(name, 'Bun')} | {cell(name, 'Go')} | {ratio(means, 'Node', pts)} | {ratio(means, 'Go', pts)} |")
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
        threads = " | ".join(cell(name, f"ParallelScript x{n}") for n in counts)
        print(f"| {titles[name]} | {threads} | {speedup} | {cell(name, 'Node')} | {cell(name, 'Bun')} | {cell(name, 'Go')} | {ratio(means, 'Node', best)} | {ratio(means, 'Go', best)} |")
runs = {len(json.load(open(f"{directory}/{name}.json"))["results"][0]["times"]) for name in names}
print(f"\nMean ± standard deviation of {'/'.join(map(str, sorted(runs)))} runs in {len(glob.glob(f'{directory}/{names[0]}.round*.json'))} interleaved rounds; wall-clock time of the whole process, its start included.")
EOF
