#!/usr/bin/env python3
"""Plain-text status snapshot of the hardening run, for the twice-daily owner update.

Read-only: inspects git, the hardening plan, the driver log and the latest session log.
"""
from __future__ import annotations

import glob
import json
import re
import subprocess
import time
from pathlib import Path

REPO = Path.home() / "projects/parallelscript"
STATE = Path.home() / ".local/state/pts-hardening"


def sh(*args: str) -> str:
    try:
        return subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=60).stdout.strip()
    except Exception as exc:  # report, never crash the cron job
        return f"<{type(exc).__name__}>"


def main() -> None:
    print("now:", time.strftime("%Y-%m-%d %H:%M %Z"))
    print("driver service:", sh("systemctl", "--user", "is-active", "pts-hardening"))
    print("PAUSE file present:", (STATE / "PAUSE").exists())
    print("HARDENING_DONE present:", (REPO / "docs/agent/HARDENING_DONE").exists())

    plan = (REPO / "docs/agent/HARDENING_PLAN.md").read_text()
    top = re.findall(r"^- \[([ x])\] \*\*(H\d+)\b", plan, re.M)
    done = [t for c, t in top if c == "x"]
    print(f"top-level tasks done: {len(done)}/{len(top)}")
    open_items = re.findall(r"^\s*- \[ \] \*\*(H\d+[a-z0-9]*) ([^*]+)\*\*", plan, re.M)
    leaf = [(i, t) for i, t in open_items if not re.search(rf"^\s*- \[[ x]\] \*\*{re.escape(i)}[a-z0-9]", plan, re.M)]
    print("current task:", f"{leaf[0][0]} {leaf[0][1].strip()}" if leaf else "none")
    print("next tasks:", "; ".join(f"{i} {t.strip()}" for i, t in leaf[1:4]))

    since = sh("git", "log", "--since=14 hours ago", "--format=%h %ad %s", "--date=format:%m-%d %H:%M")
    print("commits in last 14h:")
    print("\n".join("  " + l[:140] for l in since.splitlines()) or "  none")
    print("uncommitted files:", len(sh("git", "status", "--short").splitlines()))

    log = STATE / "driver.log"
    lines = log.read_text().splitlines()[-8:] if log.exists() else []
    print("driver log tail:")
    print("\n".join("  " + l[:160] for l in lines))
    starts = [l for l in lines if " started " in l and "session" in l]
    if starts:
        ts = time.mktime(time.strptime(starts[-1][:19], "%Y-%m-%d %H:%M:%S"))
        print(f"hours since last session start: {(time.time() - ts) / 3600:.1f}")

    files = sorted(glob.glob(str(STATE / "session-*.jsonl")))
    if files:
        lim, models = {}, set()
        for raw in open(files[-1], errors="replace"):
            try:
                d = json.loads(raw)
            except ValueError:
                continue
            m = d.get("message")
            if isinstance(m, dict) and m.get("model") and m["model"] != "<synthetic>":
                models.add(m["model"])
            if d.get("type") == "rate_limit_event":
                for k, v in ((d.get("rate_limit_info") or {}).get("unifiedWindows") or {}).items():
                    lim[k] = (max(lim.get(k, (0, ""))[0], v.get("utilization", 0)),
                              time.strftime("%m-%d %H:%M", time.localtime(v.get("resetsAt", 0))))
        print("current session models:", sorted(models))
        print("claude plan usage (utilization, resets):", lim or "n/a (non-Claude model)")


if __name__ == "__main__":
    main()
