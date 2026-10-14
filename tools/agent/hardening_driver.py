#!/usr/bin/env python3
"""Sequential GPT-only post-M7 sessions; durable systemd driver.

Never changes the normal Hermes model or invokes another inference model.
"""
from __future__ import annotations

import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import threading
import time

from driver import REPO, session_env

MODEL = "gpt-6.1-sol"
ROOT = Path.home() / ".local/state/pts-hardening"
PLAN = REPO / "docs/agent/HARDENING_PLAN.md"
DONE = REPO / "docs/agent/HARDENING_DONE"
PROMPT = """CURRENT OWNER TASK: continue the post-M7 hardening phase, not the old M0-M7 build.
All inference work MUST use gpt-6.1-sol at xhigh, including subagents. No other model is authorised.
Read docs/agent/HARDENING_PLAN.md FIRST; it overrides the historical phase/stop rule in CLAUDE.md.
Then read PROGRESS.md, decisions and the relevant spec/review evidence. Historical docs/agent/DONE
is NOT a reason to stop. Work on ONLY the first unchecked hardening task, or a clearly documented
vertical slice split from it. Use the named gpt-reproducer and gpt-reviewer subagents on every
substantive task; specify model gpt-6.1-sol explicitly for any additional Agent call. Their effort
is xhigh. Writing children must have isolated worktrees; no shared dirty tree. Parent verifies
child claims by running tests itself. Follow TDD RED-GREEN-REFACTOR and preserve failing evidence.
Commit and push green work to origin main according to the local project workflow. Verify exact
SHA CI. Update plan, progress, docs and consolidated review findings; do not alter tools/agent/.
Every result must be backed by real execution. Never tick blocked/deferred scope as finished.
After completing one task/slice, checkpoint and end SESSION_RESULT: done=<id> next=<id> green=<yes|no>.
If GPT quota is unavailable, checkpoint and exit; the driver retries GPT only. Never switch models.
No asking the owner for routine decisions: act within the supplied plan and locked constraints.
"""
AGENTS = {
    "gpt-reproducer": {
        "description": "Reproduce current ParallelScript defect; design failing regression, GPT-only",
        "model": MODEL,
        "prompt": "Use gpt-6.1-sol xhigh. Follow the parent's hardening task scope and plan. Reproduce before implementation; report commands, exit codes and exact evidence. Never tick tasks or commit the parent's dirty work. Writing requires an isolated worktree explicitly assigned by parent. Do not use other models or inspect secrets.",
    },
    "gpt-reviewer": {
        "description": "Independent read-only review of the current hardening slice; GPT-only",
        "model": MODEL,
        "prompt": "Use gpt-6.1-sol xhigh. Review only the supplied slice for correctness, race/GC safety, JS semantics, regressions and test quality. Read-only; never edit/commit/tick tasks. Return findings with file/line and runnable repro; distinguish verified evidence from suspicions. No other models or secret access.",
    },
}


def env() -> dict:
    e = session_env(True)
    e.update({
        "CLAUDE_CODE_EFFORT_LEVEL": "xhigh",
        "CLAUDE_CODE_SUBAGENT_MODEL": MODEL,
        "ANTHROPIC_DEFAULT_OPUS_MODEL": MODEL,
        "ANTHROPIC_DEFAULT_SONNET_MODEL": MODEL,
        "ANTHROPIC_DEFAULT_HAIKU_MODEL": MODEL,
        "GOMAXPROCS": "4",
    })
    return e


def cmd(prompt: str = PROMPT) -> list[str]:
    return ["claude", "-p", prompt, "--model", MODEL, "--effort", "xhigh",
            "--dangerously-skip-permissions", "--settings", str(REPO / "tools/agent/claude-settings.json"),
            "--agents", json.dumps(AGENTS), "--max-turns", "220",
            "--output-format", "stream-json", "--verbose"]


def parse_event(line: str) -> dict | None:
    try:
        d = json.loads(line)
        return d if isinstance(d, dict) else None
    except (ValueError, TypeError):
        return None


def has_open_tasks(plan: str) -> bool:
    import re
    return bool(re.search(r"^\s*- \[ \]", plan, re.M))


def complete() -> bool:
    return DONE.is_file() and not has_open_tasks(PLAN.read_text())


def log(text: str) -> None:
    ROOT.mkdir(parents=True, exist_ok=True)
    line = time.strftime("%Y-%m-%d %H:%M:%S ") + text
    print(line, flush=True)
    with (ROOT / "driver.log").open("a") as f:
        f.write(line + "\n")


def notify(text: str) -> None:
    log(text)
    try:
        p = subprocess.run([str(Path.home() / ".local/bin/hermes"), "send", "-q", "-t",
                            "telegram:-1004441160190:228", "[pts-hardening] " + text],
                           capture_output=True, timeout=60)
        if p.returncode:
            log("notification failed; driver continues")
    except Exception as exc:
        log(f"notification failed: {type(exc).__name__}")


def git_head() -> str:
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip()


def stop_group(p: subprocess.Popen) -> None:
    try:
        os.killpg(p.pid, signal.SIGTERM)
        p.wait(timeout=45)
    except subprocess.TimeoutExpired:
        os.killpg(p.pid, signal.SIGKILL)
        p.wait()
    except ProcessLookupError:
        pass


def run_once(n: int, timeout: float = 4 * 3600) -> dict:
    ROOT.mkdir(parents=True, exist_ok=True)
    path = ROOT / f"session-{n:04d}-{time.strftime('%Y%m%d-%H%M%S')}.jsonl"
    before = git_head()
    result = None
    last = ""
    mismatch = False
    p = subprocess.Popen(cmd(), cwd=REPO, env=env(), stdout=subprocess.PIPE,
                         stderr=subprocess.STDOUT, start_new_session=True)
    assert p.stdout is not None
    timer = threading.Timer(timeout, lambda: stop_group(p))
    timer.daemon = True
    timer.start()
    log(f"session {n} started {MODEL} xhigh; log={path.name}")
    try:
        with path.open("wb") as f:
            for raw in iter(p.stdout.readline, b""):
                f.write(raw)
                f.flush()
                d = parse_event(raw.decode("utf-8", "replace"))
                if not d:
                    continue
                if d.get("type") == "result":
                    result = d
                m = d.get("message", {}).get("model") if isinstance(d.get("message"), dict) else None
                if m and m != MODEL:
                    mismatch = True
                    log(f"STOP: unapproved model reported: {m}")
                    stop_group(p)
                    break
                last = str(d.get("result", d.get("error", ""))) or last
        p.wait()
    finally:
        timer.cancel()
        if p.poll() is None:
            stop_group(p)
    report = {
        "session": n, "rc": p.returncode, "model": MODEL, "model_mismatch": mismatch,
        "progressed": git_head() != before, "result": result,
        "duration_hint": "see result.duration_ms", "log": str(path),
    }
    log(f"session {n} ended rc={p.returncode} progressed={report['progressed']} "
        f"result={result.get('subtype') if result else 'missing'}")
    (ROOT / "last-result.json").write_text(json.dumps(report, indent=2))
    return report


def probe() -> bool:
    try:
        p = subprocess.run(["claude", "-p", "Reply exactly ok", "--model", MODEL,
                            "--effort", "xhigh", "--max-turns", "1", "--output-format", "json"],
                           cwd=REPO, env=env(), capture_output=True, text=True, timeout=180)
        d = parse_event(p.stdout)
        return p.returncode == 0 and bool(d) and not d.get("is_error") and d.get("subtype") == "success"
    except (OSError, subprocess.TimeoutExpired):
        return False


def main() -> None:
    ROOT.mkdir(parents=True, exist_ok=True)
    with (ROOT / "driver.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = ROOT / "state.json"
        n = json.loads(state.read_text()).get("sessions", 0) if state.exists() else 0
        idle = 0
        unavailable_notified = False
        notify("GPT-only hardening driver started; ordered tasks H01-H34, xhigh, subagents")
        while not complete():
            if (ROOT / "PAUSE").exists():
                time.sleep(60)
                continue
            if not probe():
                if not unavailable_notified:
                    notify("GPT unavailable; checkpoint preserved, retry in 15 minutes. No model fallback.")
                    unavailable_notified = True
                time.sleep(900)
                continue
            unavailable_notified = False
            n += 1
            state.write_text(json.dumps({"sessions": n, "model": MODEL}))
            r = run_once(n)
            if r["model_mismatch"]:
                (ROOT / "PAUSE").touch()
                notify("Paused: an unapproved model was reported. No further inference started.")
                continue
            idle = 0 if r["progressed"] else idle + 1
            if idle >= 3:
                notify("Three sessions without commits; checkpoint retained, retrying in one hour.")
                time.sleep(3600)
                idle = 0
            elif r["rc"] or not r["result"] or r["result"].get("is_error"):
                time.sleep(900)
            else:
                time.sleep(20)
        notify("All hardening tasks checked and HARDENING_DONE present; driver exiting.")


if __name__ == "__main__":
    main()
