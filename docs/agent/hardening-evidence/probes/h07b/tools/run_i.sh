#!/bin/bash
# Facet i sweep: Request.signal first read on a task vs. client disconnect.
for spins in 20000 200000 1000000; do
  for t in 2 4; do
    for r in 1 2; do
      echo "== MAX_SPINS=$spins threads=$t run=$r"
      REQUESTS=3000 MAX_SPINS=$spins PTS_THREADS=$t timeout 120 /tmp/h07b/repro/bin/c116-request-signal-disconnect 2>&1 | head -c 600
      echo "exit=${PIPESTATUS[0]}"
    done
  done
done
