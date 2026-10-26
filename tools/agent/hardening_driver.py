#!/usr/bin/env python3
"""Sequential post-M7 hardening sessions; durable systemd driver.

Owner directive 2026-10-18: primary claude-opus-5-5 (native login), falling back to
gpt-6.1-sol and then grok-4.7 (CLIProxyAPI) when the primary is limited or down; return
to Opus as soon as it is available again. 500 turns per session, xhigh effort.
"""
from __future__ import annotations

import fcntl
import re
import json
import os
from pathlib import Path
import signal
import subprocess
import threading
import time

from driver import REPO, session_env

CHAIN = [  # (model id, via CLIProxyAPI) in priority order
    ("claude-opus-5-5", False),
    ("gpt-6.1-sol", True),
    ("grok-4.7", True),
]
MODEL = CHAIN[0][0]
VIA_PROXY = CHAIN[0][1]
MAX_TURNS = 500
SESSION_TIMEOUT = 8 * 3600
# Usage budget (owner, 2026-10-26: "be more reasonable in using the limit weekly and stop when we
# are using too much"). Claude plan utilization comes from Claude Code's rate_limit_event.
WEEK = 7 * 24 * 3600
OPUS_WEEKLY_CAP = 0.70      # the driver never takes the weekly window past this; the rest is the owner's
OPUS_HEADSTART = 0.10       # allowance available at the start of a week
OPUS_FIVE_HOUR_START = 0.70 # don't start an Opus session above this 5-hour utilization
OPUS_HARD_STOP = OPUS_WEEKLY_CAP + 0.05  # stop a running session past this weekly utilization
FALLBACK_SESSIONS_PER_DAY = 3  # gpt-6.1-sol / grok-4.7: no utilization signal, so cap sessions per day
LIMIT_RE = re.compile(r"usage limit|rate.?limit|limit reached|hit your limit|quota|429|overloaded|"
                      r"insufficient|exhausted|credit balance|too many requests", re.I)
ROOT = Path.home() / ".local/state/pts-hardening"
PLAN = REPO / "docs/agent/HARDENING_PLAN.md"
DONE = REPO / "docs/agent/HARDENING_DONE"
PROMPT = """CURRENT OWNER TASK: continue the post-M7 hardening phase, not the old M0-M7 build.
You are running as {model} at xhigh effort. Owner directive 2026-10-18: claude-opus-5-5 is the
primary model; gpt-6.1-sol and then grok-4.7 are approved fallbacks when it is limited. Subagents
use the same model as you ({model}); specify it explicitly for any additional Agent call.
Read docs/agent/HARDENING_PLAN.md FIRST; it overrides the historical phase/stop rule in CLAUDE.md.
Then read PROGRESS.md, decisions and the relevant spec/review evidence. Historical docs/agent/DONE
is NOT a reason to stop. Work on the first unchecked ([ ]) unblocked task; once it is done and pushed,
continue with the next one while your context stays under ~60%. Use the named reproducer and reviewer subagents on every
substantive task. Writing children must have isolated worktrees; no shared dirty tree. Parent
verifies child claims by running tests itself. Follow TDD RED-GREEN-REFACTOR and preserve failing
evidence. You have up to 500 turns: commit and push each green slice as soon as it is green rather
than holding it to the end, so a session that runs out of turns never loses work.
Commit and push green work to origin main according to the local project workflow. Run the full
suite with 4 parallel jobs (-p 4 -parallel 4, GOMAXPROCS=4). NEVER wait or poll for GitHub CI:
check recent runs once at session start (non-blocking), fix any failed run first, backfill run
IDs, and otherwise tick a slice on a green local suite with "CI: pending".
Update plan, progress, docs and consolidated review findings; do not alter tools/agent/.
Every result must be backed by real execution. Never tick blocked/deferred scope as finished.
SPLIT AND TIME-BOX RULES (owner, 2026-10-22; these override "no scope dropped" wording in the plan):
- Never create a task ID longer than the top-level ID plus three suffix characters (H09b5c, H12a1).
  If more structure is needed, use a plain bullet list inside the task, not deeper IDs.
- Never split a remainder again. If a slice can't be finished: when what remains is correctness or
  memory safety, keep working on it; when it is precision, performance, generated-code size or other
  refinement where programs already behave correctly, record it in docs/agent/DEFERRED.md (reproducer,
  skipped test if useful, owning later task such as H25/H27), mark it `- [~]` with a pointer, and move on.
- A top-level task that has used 6 sessions without finishing is time-boxed the same way.
- `[~]` is not `[x]`: deferred scope stays listed in DEFERRED.md and in HARDENING_DONE.
Before ending, checkpoint and end with SESSION_RESULT: done=<ids> next=<id> green=<yes|no>.
If the model's usage limit is hit, checkpoint and exit; the driver switches models.
No asking the owner for routine decisions: act within the supplied plan and locked constraints.
If a task is refused by the model provider, record it as blocked with the exact error, do not
rephrase it to get around the refusal, and move to the next independent task.
"""
AGENT_PROMPTS = {
    "reproducer": ("Reproduce a current ParallelScript defect and design a failing regression",
                   "Follow the parent's hardening task scope and plan. Reproduce before implementation; report commands, exit codes and exact evidence. Never tick tasks or commit the parent's dirty work. Writing requires an isolated worktree explicitly assigned by parent. Do not inspect secrets."),
    "reviewer": ("Independent read-only review of the current hardening slice",
                 "Review only the supplied slice for correctness, race/GC safety, JS semantics, regressions and test quality. Read-only; never edit/commit/tick tasks. Return findings with file/line and runnable repro; distinguish verified evidence from suspicions. No secret access."),
}


def agents(model: str = MODEL) -> dict:
    return {name: {"description": d, "model": model, "prompt": pr} for name, (d, pr) in AGENT_PROMPTS.items()}


AGENTS = agents()


def via_proxy(model: str) -> bool:
    return dict(CHAIN)[model]


def env(model: str = MODEL) -> dict:
    e = session_env(via_proxy(model))
    e.update({
        "CLAUDE_CODE_EFFORT_LEVEL": "xhigh",
        "CLAUDE_CODE_SUBAGENT_MODEL": model,
        "ANTHROPIC_DEFAULT_OPUS_MODEL": model,
        "ANTHROPIC_DEFAULT_SONNET_MODEL": model,
        "ANTHROPIC_DEFAULT_HAIKU_MODEL": model,
        "GOMAXPROCS": "4",
    })
    return e


def cmd(model: str = MODEL) -> list[str]:
    return ["claude", "-p", PROMPT.format(model=model), "--model", model, "--effort", "xhigh",
            "--dangerously-skip-permissions", "--settings", str(REPO / "tools/agent/claude-settings.json"),
            "--agents", json.dumps(agents(model)), "--max-turns", str(MAX_TURNS),
            "--output-format", "stream-json", "--verbose"]


def parse_event(line: str) -> dict | None:
    try:
        d = json.loads(line)
        return d if isinstance(d, dict) else None
    except (ValueError, TypeError):
        return None


def unapproved_model(event: dict, expected: str = MODEL) -> str | None:
    message = event.get("message")
    if not isinstance(message, dict):
        return None
    model = message.get("model")
    # Claude Code labels locally generated API/error notices this way.
    # It is not an inference provider or an alternative selected model.
    return model if isinstance(model, str) and model not in (expected, "<synthetic>") else None


def limit_reset(event: dict) -> float | None:
    """Reset time if this event says the plan limit rejected us, else None."""
    if event.get("type") != "rate_limit_event":
        return None
    info = event.get("rate_limit_info") or {}
    if info.get("status") != "rejected":
        return None
    return float(info.get("resetsAt") or time.time() + 3600)


def windows(event: dict) -> dict | None:
    """{'five_hour': (util, resetsAt), 'seven_day': (util, resetsAt)} from a rate_limit_event."""
    if event.get("type") != "rate_limit_event":
        return None
    w = (event.get("rate_limit_info") or {}).get("unifiedWindows") or {}
    out = {k: (float(v.get("utilization") or 0), float(v.get("resetsAt") or 0))
           for k, v in w.items() if isinstance(v, dict)}
    return out or None


def weekly_allowance(reset_at: float, now: float | None = None) -> float:
    """Linear pacing: share of the week elapsed times the cap, plus a head start, capped."""
    now = time.time() if now is None else now
    elapsed = max(0.0, min(1.0, 1 - (reset_at - now) / WEEK))
    return min(OPUS_WEEKLY_CAP, OPUS_HEADSTART + OPUS_WEEKLY_CAP * elapsed)


def budget_ok(win: dict | None, now: float | None = None) -> tuple[bool, str, float]:
    """May an Opus session start? Returns (ok, reason, retry_at)."""
    now = time.time() if now is None else now
    if not win or "seven_day" not in win:
        return True, "no utilization data", now
    wk, wk_reset = win["seven_day"]
    allowed = weekly_allowance(wk_reset, now)
    if wk >= allowed:
        # when will the allowance catch up with current use?
        if wk >= OPUS_WEEKLY_CAP:
            retry = wk_reset + 60
        else:
            need = (wk - OPUS_HEADSTART) / OPUS_WEEKLY_CAP  # elapsed fraction needed
            retry = wk_reset - WEEK + need * WEEK + 60
        return False, f"weekly {wk:.0%} >= paced allowance {allowed:.0%}", max(retry, now + 900)
    fh = win.get("five_hour")
    if fh and fh[0] >= OPUS_FIVE_HOUR_START:
        return False, f"5-hour window {fh[0]:.0%} >= {OPUS_FIVE_HOUR_START:.0%}", max(fh[1] + 60, now + 900)
    return True, f"weekly {wk:.0%} < allowance {allowed:.0%}", now


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


def run_once(n: int, model: str = MODEL, timeout: float = SESSION_TIMEOUT) -> dict:
    ROOT.mkdir(parents=True, exist_ok=True)
    path = ROOT / f"session-{n:04d}-{time.strftime('%Y%m%d-%H%M%S')}.jsonl"
    before = git_head()
    result = None
    last = ""
    mismatch = False
    reset_at = None
    over_budget = False
    p = subprocess.Popen(cmd(model), cwd=REPO, env=env(model), stdout=subprocess.PIPE,
                         stderr=subprocess.STDOUT, start_new_session=True)
    assert p.stdout is not None
    timer = threading.Timer(timeout, lambda: stop_group(p))
    timer.daemon = True
    timer.start()
    log(f"session {n} started {model} xhigh max_turns={MAX_TURNS}; log={path.name}")
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
                if d.get("type") == "assistant" and isinstance(d.get("message"), dict):
                    for x in d["message"].get("content") or []:
                        if isinstance(x, dict) and x.get("type") == "text":
                            last = str(x.get("text", ""))[-500:]
                r_at = limit_reset(d)
                if r_at:
                    reset_at = r_at
                w = windows(d)
                if w:
                    (ROOT / "usage.json").write_text(json.dumps({"model": model, "at": time.time(), **w}))
                    if model == CHAIN[0][0] and w.get("seven_day", (0, 0))[0] >= OPUS_HARD_STOP:
                        over_budget = True
                        log(f"STOP: weekly utilization {w['seven_day'][0]:.0%} past the driver's hard cap "
                            f"{OPUS_HARD_STOP:.0%}; ending the session (work is committed slice by slice)")
                        stop_group(p)
                        break
                m = unapproved_model(d, model)
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
    text = str((result or {}).get("result", "")) + " " + last
    limited = bool(reset_at) or bool(result and result.get("is_error") and LIMIT_RE.search(text))
    report = {
        "session": n, "rc": p.returncode, "model": model, "model_mismatch": mismatch,
        "limited": limited, "reset_at": reset_at, "over_budget": over_budget,
        "progressed": git_head() != before, "result": result,
        "duration_hint": "see result.duration_ms", "log": str(path),
    }
    log(f"session {n} ended model={model} rc={p.returncode} progressed={report['progressed']} "
        f"limited={limited} result={result.get('subtype') if result else 'missing'}")
    (ROOT / "last-result.json").write_text(json.dumps(report, indent=2))
    return report


LAST_WINDOWS: dict = {}


def probe(model: str) -> tuple[bool, str]:
    try:
        p = subprocess.run(["claude", "-p", "Reply exactly ok", "--model", model,
                            "--effort", "low", "--max-turns", "1", "--output-format", "stream-json",
                            "--verbose"],
                           cwd=REPO, env=env(model), capture_output=True, text=True, timeout=180)
        result = None
        for line in p.stdout.splitlines():
            d = parse_event(line)
            if not d:
                continue
            if d.get("type") == "result":
                result = d
            w = windows(d)
            if w:
                LAST_WINDOWS[model] = w
        ok = p.returncode == 0 and bool(result) and not result.get("is_error") and result.get("subtype") == "success"
        return ok, (str((result or {}).get("result", "")) + p.stderr[-300:])
    except (OSError, subprocess.TimeoutExpired) as exc:
        return False, type(exc).__name__


def fallback_sessions_today(model: str) -> int:
    today = time.strftime("%Y-%m-%d")
    log_path = ROOT / "driver.log"
    if not log_path.exists():
        return 0
    return sum(1 for l in log_path.read_text().splitlines()
               if l.startswith(today) and " started " in l and f" {model} " in l)


def pick_model(cooldown: dict, notes: list | None = None) -> str | None:
    """First model in CHAIN that is off cooldown, within budget and answers a probe."""
    for model, _ in CHAIN:
        if cooldown.get(model, 0) > time.time():
            continue
        if model != CHAIN[0][0] and fallback_sessions_today(model) >= FALLBACK_SESSIONS_PER_DAY:
            if notes is not None:
                notes.append(f"{model}: {FALLBACK_SESSIONS_PER_DAY} sessions today already")
            continue
        ok, txt = probe(model)
        if ok and model == CHAIN[0][0]:
            fine, why, retry = budget_ok(LAST_WINDOWS.get(model))
            if not fine:
                cooldown[model] = retry
                log(f"budget: {model} paused until {time.strftime('%m-%d %H:%M', time.localtime(retry))} ({why})")
                if notes is not None:
                    notes.append(f"{model}: {why}, resumes {time.strftime('%a %H:%M', time.localtime(retry))}")
                continue
        if ok:
            return model
        wait = 3600 if LIMIT_RE.search(txt) else 900
        cooldown[model] = time.time() + wait
        log(f"probe failed for {model}; cooldown {wait // 60} min :: {txt[:160]!r}")
    return None


def main() -> None:
    ROOT.mkdir(parents=True, exist_ok=True)
    with (ROOT / "driver.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = ROOT / "state.json"
        n = json.loads(state.read_text()).get("sessions", 0) if state.exists() else 0
        idle = 0
        cooldown: dict[str, float] = {}
        current = None
        none_notified = False
        notify("Hardening driver started: claude-opus-5-5 -> gpt-6.1-sol -> grok-4.7, "
               f"xhigh, {MAX_TURNS} turns/session; budget: Opus paced to {OPUS_WEEKLY_CAP:.0%} of the week, "
               f"fallbacks {FALLBACK_SESSIONS_PER_DAY} sessions/day each")
        while not complete():
            if (ROOT / "PAUSE").exists():
                time.sleep(60)
                continue
            notes: list = []
            model = pick_model(cooldown, notes)
            if not model:
                if not none_notified:
                    notify("Paused for budget/limits: " + "; ".join(notes or ["all models limited"]) +
                           ". Checkpoint kept; rechecking every 30 minutes.")
                    none_notified = True
                time.sleep(1800)
                continue
            none_notified = False
            if model != current:
                if current is not None:
                    notify(f"Switching model: {current} -> {model}")
                current = model
            n += 1
            state.write_text(json.dumps({"sessions": n, "model": model}))
            r = run_once(n, model)
            if r["model_mismatch"]:
                (ROOT / "PAUSE").touch()
                notify("Paused: a model outside the approved chain was reported. No further inference started.")
                continue
            if r.get("over_budget"):
                cooldown[model] = (LAST_WINDOWS.get(model, {}).get("seven_day", (0, time.time() + 3600))[1] + 60)
                notify(f"{model} reached the driver's weekly cap ({OPUS_HARD_STOP:.0%}); session stopped, "
                       "Opus paused until the weekly reset.")
                continue
            if r["limited"]:
                until = r["reset_at"] or time.time() + 3600
                cooldown[model] = until
                log(f"{model} limited; cooldown until {time.strftime('%m-%d %H:%M', time.localtime(until))}")
                continue
            idle = 0 if r["progressed"] else idle + 1
            if idle >= 3:
                notify("Three sessions without commits; checkpoint retained, retrying in one hour.")
                time.sleep(3600)
                idle = 0
            elif r["rc"] and not r["progressed"] and (r["result"] or {}).get("subtype") != "error_max_turns":
                time.sleep(900)
            else:
                time.sleep(20)
        notify("All hardening tasks checked and HARDENING_DONE present; driver exiting.")


if __name__ == "__main__":
    main()
