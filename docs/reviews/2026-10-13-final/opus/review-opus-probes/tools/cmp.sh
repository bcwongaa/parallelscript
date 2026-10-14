#!/bin/bash
# cmp.sh file.pts [threads]: build with pts, run it (10 s limit), run the same file under Node
# (--experimental-transform-types) and diff the two. NONODE=1 skips Node (ParallelScript-only probes).
# Override PTS (the compiler), NODE and OUT (scratch directory) if yours differ.
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../.." && pwd)
export PTS_HOME=${PTS_HOME:-$repo} PTS_CACHE_DIR=${PTS_CACHE_DIR:-$HOME/.cache/pts}
PTS=${PTS:-$repo/pts-bin/pts}; NODE=${NODE:-$HOME/.local/node/bin/node}; OUT=${OUT:-/tmp/pts-review-opus-rerun}
f=$1; t=${2:-4}; mkdir -p "$OUT"; b=$OUT/$(basename "${f%.pts}")
if ! "$PTS" build "$f" -o "$b" > "$b.compile" 2>&1; then echo "== COMPILE ERROR: $f"; cat "$b.compile"; exit 1; fi
PTS_THREADS=$t timeout 10 "$b" > "$b.pout" 2>&1; pe=$?
if [ -n "$NONODE" ]; then echo "== exit=$pe"; cat "$b.pout"; exit 0; fi
cp "$f" "$b.ts"
timeout 10 "$NODE" --experimental-transform-types --no-warnings "$b.ts" > "$b.nout" 2>&1; ne=$?
if cmp -s "$b.pout" "$b.nout" && [ $pe = $ne ]; then echo "== SAME (exit $pe): $f"; else echo "== DIFF pts=$pe node=$ne: $f"; diff "$b.pout" "$b.nout" | head -30; fi
