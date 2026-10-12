#!/usr/bin/env bash
# Benchmarks HTTP servers: ParallelScript's pts:http against Node's node:http, Bun.serve
# and Go's net/http, loading each with oha, and prints a Markdown table of requests per
# second for docs/BENCHMARKS.md (D-137).
#
# Usage: pts/bench/serve.sh [name ...]    (default: every server benchmark)
#
# A server benchmark is a directory pts/bench/<name>/ with a file `request`: its first
# line is the method and path oha sends ("GET /"), the rest is the body. main.pts serves
# with reusePort and runs under each PTS_THREADS listed in `threads`; main.ts is the Node
# server, bun.ts the Bun one and main.go the Go one. Each listens on $PORT, and all must
# answer the request with the same body.
# Environment: DURATION (default 10s) of each measured run, after WARMUP (default 2s) of
# load; CONNECTIONS (default 64) kept open by oha; RESULTS (default
# pts/bench/results/latest) keeps oha's JSON report of each run, as <name>-<i>.json with
# the server's name in <name>-<i>.label.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
bench=$root/pts/bench
work=$(mktemp -d)
results=${RESULTS:-$bench/results/latest}
mkdir -p "$results"
server=""
cleanup() {
  if [ -n "$server" ]; then kill "$server" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

for tool in oha curl go node bun clang python3; do
  command -v "$tool" >/dev/null || { echo "serve.sh: $tool is not on PATH" >&2; exit 1; }
done

if [ $# -gt 0 ]; then
  names=("$@")
else
  names=()
  for dir in "$bench"/*/; do
    if [ -f "$dir/request" ]; then names+=("$(basename "$dir")"); fi
  done
fi

echo "Building pts..." >&2
go build -o "$work/pts" "$root/cmd/pts"
export PTS_HOME=$root

# freePort prints a TCP port nothing listens on.
freePort() {
  python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'
}

# ask sends the benchmark's request to url once and prints the response's body.
ask() {
  curl -s --max-time 5 -X "$method" ${body:+--data-binary "@$body" -H "content-type: application/json"} "$1"
}

# awaitListening waits up to 10 s for a server to answer the benchmark's request at url.
awaitListening() {
  for _ in $(seq 100); do
    if ask "$1" > /dev/null; then return 0; fi
    sleep 0.1
  done
  echo "serve.sh: nothing answers at $1" >&2
  return 1
}

for name in "${names[@]}"; do
  dir=$bench/$name
  echo "== $name" >&2
  read -r method path < "$dir/request"
  body=""
  if [ "$(wc -l < "$dir/request")" -gt 1 ]; then
    body=$work/$name.body
    tail -n +2 "$dir/request" > "$body"
  fi
  "$work/pts" build "$dir/main.pts" -o "$work/$name-pts"
  (cd "$root" && go build -o "$work/$name-go" "./pts/bench/$name")
  labels=()
  commands=()
  for threads in $(cat "$dir/threads"); do
    labels+=("ParallelScript x$threads")
    commands+=("env PTS_THREADS=$threads $work/$name-pts")
  done
  labels+=(Node Bun Go)
  commands+=("node --no-warnings $dir/main.ts" "bun $dir/bun.ts" "$work/$name-go")

  expected=""
  for i in "${!commands[@]}"; do
    port=$(freePort)
    url=http://127.0.0.1:$port$path
    env PORT="$port" ${commands[$i]} > "$work/server.log" 2>&1 &
    server=$!
    awaitListening "$url"
    # Every server must answer the same.
    actual=$(ask "$url")
    if [ -z "$expected" ]; then expected=$actual; fi
    if [ "$actual" != "$expected" ]; then
      echo "serve.sh: $name: ${labels[$i]} answers differently:" >&2
      diff <(echo "$expected") <(echo "$actual") >&2 || true
      exit 1
    fi
    load=(-c "${CONNECTIONS:-64}" -m "$method" --no-tui)
    if [ -n "$body" ]; then load+=(-D "$body" -T application/json); fi
    oha -z "${WARMUP:-2s}" "${load[@]}" --output-format quiet "$url"
    oha -z "${DURATION:-10s}" "${load[@]}" --output-format json -o "$work/$name-$i.json" "$url"
    echo "${labels[$i]}" > "$work/$name-$i.label"
    cp "$work/$name-$i.json" "$work/$name-$i.label" "$results/"
    kill "$server"
    wait "$server" 2>/dev/null || true
    server=""
  done
done

# A row per benchmark: requests per second for each server, the speedup of the most
# loops over one, and how ParallelScript with the most loops compares with the others.
python3 - "$work" "${names[@]}" <<'EOF'
import glob, json, os, sys

work, names = sys.argv[1], sys.argv[2:]
results = {}
for name in names:
    results[name] = {}
    for path in glob.glob(f"{work}/{name}-*.json"):
        with open(path.removesuffix(".json") + ".label") as f:
            label = f.read().strip()
        with open(path) as f:
            report = json.load(f)
        codes = report.get("statusCodeDistribution", {})
        if set(codes) - {"200"}:
            sys.exit(f"serve.sh: {name}: {label} answered {codes}")
        results[name][label] = report["summary"]["requestsPerSec"]

counts = sorted({int(label.split("x")[-1]) for name in names for label in results[name] if label.startswith("ParallelScript x")})
most = counts[-1]

def cell(rates, label):
    return f"{rates[label]:,.0f}" if label in rates else "–"

def ratio(rates, label, pts):
    return f"{pts / rates[label]:.2f}×" if label in rates else "–"

print("| Benchmark (requests/s) | " + " | ".join(f"PTS ×{n}" for n in counts) + f" | Speedup ×{most} | Node | Bun | Go | PTS ×{most} vs Node | PTS ×{most} vs Go |")
print("|---|" + "---|" * (len(counts) + 6))
for name in names:
    rates = results[name]
    one, best = rates.get("ParallelScript x1"), rates.get(f"ParallelScript x{most}")
    speedup = f"{best / one:.2f}×" if one and best else "–"
    loops = " | ".join(cell(rates, f"ParallelScript x{n}") for n in counts)
    print(f"| {name} | {loops} | {speedup} | {cell(rates, 'Node')} | {cell(rates, 'Bun')} | {cell(rates, 'Go')} | {ratio(rates, 'Node', best)} | {ratio(rates, 'Go', best)} |")
EOF
