#!/bin/bash
# Facet i: repeat the configurations around the hit (MAX_SPINS=20000, 4 threads).
for spins in 5000 20000 50000; do
  for t in 4 2; do
    for r in 1 2 3 4; do
      out=$(REQUESTS=3000 MAX_SPINS=$spins PTS_THREADS=$t timeout 120 /tmp/h07b/repro/bin/c116-request-signal-disconnect 2>&1 | head -c 600)
      echo "MAX_SPINS=$spins threads=$t run=$r exit=$? :: $out"
    done
  done
done
