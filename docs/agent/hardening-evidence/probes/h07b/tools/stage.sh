#!/bin/bash
# Copy H07b probes, twins and tools into the worktree's evidence folder.
set -eu
W=/home/bryan/projects/parallelscript/.claude/worktrees/agent-ac2576d660f32ee23
D=$W/docs/agent/hardening-evidence/probes/h07b
R=/tmp/h07b/repro
mkdir -p "$D/oracles" "$D/c119" "$D/c121" "$D/retention" "$D/tools"
cp "$R/probes/c116-shared-threads.pts" "$R/probes/c116-shared-threads.oracle.mts" \
   "$R/probes/c116-any-window.pts" "$R/probes/c116-fetch-abort-window.pts" \
   "$R/probes/c116-fetch-abort-sequential-pool.pts" "$R/probes/c116-request-signal-disconnect.pts" "$D/"
cp "$R/oracles/abort_signal_shared_threads.mts" "$R/oracles/abort_signal_any_cost.mts" \
   "$R/oracles/abort_signal_any_dependents.mts" "$R/oracles/fetch_abort_other_thread.mts" "$D/oracles/"
cp "$R/c119/any_cost.pts" "$R/c119/any_cost.mts" "$D/c119/"
cp "$R/c121/order_matrix.pts" "$R/c121/order_matrix.mts" "$D/c121/"
cp "$R/retention/retention.pts" "$R/retention/retention.mts" "$D/retention/"
cp "$R/tools/twin.py" "$R/tools/runp.sh" "$R/tools/summ.py" "$R/tools/run_i.sh" "$R/tools/run_i2.sh" "$R/tools/stage.sh" "$D/tools/"
ls -R "$D"
