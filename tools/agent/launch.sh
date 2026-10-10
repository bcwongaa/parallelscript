#!/usr/bin/env bash
# Hand-off launcher, run once as a detached systemd user unit (pts-driver):
#   1. wait until the stale Hermes session (20261011_010624_bb15b70b) that was building in
#      ~/projects/parallelscript has gone quiet for 15 minutes (owner chose: stop it, discard its work)
#   2. discard that working tree and move the fresh clone into place
#   3. run the session driver
set -euo pipefail
OLD=$HOME/projects/parallelscript
NEW=$HOME/projects/parallelscript.new
LOG=$HOME/.hermes/logs/agent.log
STALE=20261011_010624_bb15b70b
mkdir -p "$HOME/.local/state/pts-driver"
say() { echo "$(date '+%F %T') $*" | tee -a "$HOME/.local/state/pts-driver/driver.log"; }

if [ -d "$NEW" ]; then
  say "launcher: waiting for stale session $STALE to go quiet"
  while :; do
    last=$(grep "\[$STALE\]" "$LOG" | tail -1 | cut -c1-19 || true)
    idle=$(( $(date +%s) - $(date -d "${last:-1970-01-01}" +%s) ))
    [ "$idle" -ge 900 ] && break
    sleep 120
  done
  say "launcher: stale session idle ${idle}s; swapping working trees"
  rm -rf "$OLD"
  mv "$NEW" "$OLD"
fi
cd "$OLD"
exec python3 "$OLD/tools/agent/driver.py"
