#!/usr/bin/env python3
"""Sequential single-model post-M7 sessions; durable systemd driver.

Owner-selected model (2026-10-15): claude-opus-5-5 xhigh, native login.
Never falls back to another inference model.
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

MODEL = "claude-opus-5-5"
VIA_PROXY = False  # Opus uses the native Claude login
ROOT = Path.home() / ".local/state/pts-hardening"
PLAN = REPO / "docs/agent/HARDENING_PLAN.md"
DONE = REPO / "docs/agent/HARDENING_DONE"
PROMPT = """CURRENT OWNER TASK: continue the post-M7 hardening phase, not the old M0-M7 build.
Owner directive 2026-10-15: all remaining work uses claude-opus-5-5 at xhigh, including subagents.
No other model is authorised and there is no fallback.
Read docs/agent/HARDENING_PLAN.md FIRST; it overrides the historical phase/stop rule in CLAUDE.md.
Then read PROGRESS.md, decisions and the relevant spec/review evidence. Historical docs/agent/DONE
is NOT a reason to stop. Work on ONLY the first unchecked, unblocked hardening task, or a clearly
documented vertical slice split from it. Use the named opus-reproducer and opus-reviewer subagents
on every substantive task; specify model claude-opus-5-5 explicitly for any additional Agent call.
Writing children must have isolated worktrees; no shared dirty tree. Parent verifies child claims
by running tests itself. Follow TDD RED-GREEN-REFACTOR and preserve failing evidence.
Commit and push green work to origin main according to the local project workflow. Verify exact
SHA CI. Update plan, progress, docs and consolidated review findings; do not alter tools/agent/.
Every result must be backed by real execution. Never tick blocked/deferred scope as finished.
After completing one task/slice, checkpoint and end SESSION_RESULT: done=<id> next=<id> green=<yes|no>.
If the model's usage limit is hit, checkpoint and exit; the driver waits and retries the same model.
No asking the owner for routine decisions: act within the supplied plan and locked constraints.
If a task is refused by the model provider, record it as blocked with the exact error, do not
rephrase it to get around the refusal, and move to the next independent task.
"""
AGENTS = {
    "opus-reproducer": {
        "description": "Reproduce a current ParallelScript defect and design a failing regression",
        "model": MODEL,
        "prompt": "Follow the parent's hardening task scope and plan. Reproduce before implementation; report commands, exit codes and exact evidence. Never tick tasks or commit the parent's dirty work. Writing requires an isolated worktree explicitly assigned by parent. Do not inspect secrets.",
    },
    "opus-reviewer": {
        "description": "Independent read-only review of the current hardening slice",
        "model": MODEL,
        "prompt": "Review only the supplied slice for correctness, race/GC safety, JS semantics, regressions and test quality. Read-only; never edit/commit/tick tasks. Return findings with file/line and runnable repro; distinguish verified evidence from suspicions. No secret access.",
    },
}


def env() -> dict:
    e = session_env(VIA_PROXY)
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


def unapproved_model(event: dict) -> str | None:
    message = event.get("message")
    if not isinstance(message, dict):
        return None
    model = message.get("model")
    # Claude Code labels locally generated API/error notices this way.
    # It is not an inference provider or an alternative selected model.
    return model if isinstance(model, str) and model not in (MODEL, "<synthetic>") else None


def is_content_refusal(event: dict) -> bool:
    message = event.get("message")
    if not isinstance(message, dict) or message.get("model") != "<synthetic>":
        return False
    content = message.get("content", [])
    text = " ".join(str(x.get("text", "")) for x in content if isinstance(x, dict)).lower()
    return "flagged for possible cybersecurity risk" in text or "content policy" in text


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
                m = unapproved_model(d)
                if m:
                    mismatch = True
                    log(f"STOP: unapproved model reported: {m}")
                    stop_group(p)
                    break
                if is_content_refusal(d):
                    refusal = {"session": n, "event": d}
                    with (ROOT / "content-refusals.jsonl").open("a") as rf:
                        rf.write(json.dumps(refusal) + "\n")
                    log("Upstream content refusal recorded; not a model switch. Block the refused subtask, no evasion.")
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
        notify(f"Hardening driver started on {MODEL} xhigh; ordered tasks, subagents, no fallback")
        while not complete():
            if (ROOT / "PAUSE").exists():
                time.sleep(60)
                continue
            if not probe():
                if not unavailable_notified:
                    notify(f"{MODEL} unavailable (limit or outage); checkpoint kept, retrying every 15 minutes. No fallback.")
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
