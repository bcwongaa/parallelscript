#!/bin/bash
# runp.sh <probe.pts> <threads...>: build once with the base CLI, then run at each thread
# count, printing the exit code and output. RUNS (default 1) repeats each thread count.
# Logs go to /tmp/h07b/repro/logs/<probe>.<threads>.<run>.log
set -u
probe=$1; shift
name=$(basename "$probe" .pts)
mkdir -p /tmp/h07b/repro/logs /tmp/h07b/repro/bin
bin=/tmp/h07b/repro/bin/$name
PTS_HOME=/tmp/h07b/base /tmp/h07b/base/pts-bin/pts build "$probe" -o "$bin" || { echo "build failed: $?"; exit 2; }
for t in "$@"; do
  for r in $(seq 1 "${RUNS:-1}"); do
    log=/tmp/h07b/repro/logs/$name.t$t.r$r.log
    start=$(date +%s.%N)
    PTS_THREADS=$t timeout "${TMO:-120}" "$bin" > "$log" 2>&1
    code=$?
    end=$(date +%s.%N)
    printf '== %s threads=%s run=%s exit=%s secs=%.2f\n' "$name" "$t" "$r" "$code" "$(echo "$end - $start" | bc)"
    if [ -n "${QUIET:-}" ]; then head -c "${QUIET}" "$log"; else cat "$log"; fi
  done
done
