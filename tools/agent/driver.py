#!/usr/bin/env python3
"""ParallelScript autonomous session driver.

Runs Claude Code sessions back to back on this repo until docs/agent/DONE exists.
Each session reads CLAUDE.md -> PROGRESS.md -> docs/MASTER_PLAN.md and does the next task(s).

Model policy (owner instruction):
  1. claude-opus-5-5, effort xhigh, via the native Claude login        (preferred)
  2. gpt-6.1-sol, via the local CLIProxyAPI (Anthropic-compatible)       (fallback 1)
  3. grok-4.7,   via the local CLIProxyAPI                               (fallback 2)
  Usage-limit hits put a model on cooldown; the driver returns to Opus as soon as its
  cooldown passes (checked before every session). Fallback sessions are told to do one
  task and stop so we get back to Opus quickly.

State + logs: ~/.local/state/pts-driver/  (never in the repo)
Sessions must not edit this file.
"""
from __future__ import annotations

import json
import os
import re
import signal
import subprocess
import sys
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

REPO = Path(os.environ.get("PTS_REPO", Path.home() / "projects/parallelscript"))
STATE_DIR = Path.home() / ".local/state/pts-driver"
STATE = STATE_DIR / "state.json"
LOGS = STATE_DIR / "logs"
DONE = REPO / "docs/agent/DONE"
PAUSE = STATE_DIR / "PAUSE"  # touch to pause between sessions
TG_TARGET = "telegram:-1004441160190:228"
HKT = timezone(timedelta(hours=8))
PROXY = "http://127.0.0.1:8317"

MODELS = [  # (id, via_proxy, session_timeout_s)
    ("claude-opus-5-5", False, 5 * 3600),
    ("gpt-6.1-sol", True, 3 * 3600),
    ("grok-4.7", True, 3 * 3600),
]
DEFAULT_COOLDOWN = {"claude-opus-5-5": 45 * 60, "gpt-6.1-sol": 30 * 60, "grok-4.7": 30 * 60}
LIMIT_RE = re.compile(r"usage limit|rate.?limit|limit reached|hit your limit|quota|429|overloaded|"
                      r"insufficient|exhausted|credit balance|too many requests", re.I)
MAX_IDLE_SESSIONS = 3  # consecutive sessions with no new commit -> alert + long sleep

KICKOFF = """You are an autonomous session in the ParallelScript build (model: {model}).
Follow the session protocol in CLAUDE.md exactly: read CLAUDE.md, PROGRESS.md, docs/MASTER_PLAN.md,
docs/DECISIONS.md (or docs/agent/PREDECISIONS.md) first; check git status; run the baseline; then do
the first unchecked task in docs/MASTER_PLAN.md end to end (tests, docs, commit, push).
{scope}
Never wait for the owner. Do not edit tools/agent/. End with the SESSION_RESULT line."""
SCOPE_PRIMARY = ("Keep going task after task while your context stays under ~60%, then stop cleanly "
                 "with everything committed and pushed.")
SCOPE_FALLBACK = ("You are a FALLBACK model: complete exactly ONE task (or one clearly-defined split part "
                  "of it), leave thorough notes in PROGRESS.md, commit, push, and stop.")


def now() -> float:
    return time.time()


def log(msg: str) -> None:
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    line = f"{datetime.now(HKT):%Y-%m-%d %H:%M:%S} {msg}"
    print(line, flush=True)
    with open(STATE_DIR / "driver.log", "a") as f:
        f.write(line + "\n")


def notify(msg: str) -> None:
    log(f"notify: {msg}")
    try:
        subprocess.run(["hermes", "send", "-q", "-t", TG_TARGET, f"[pts-driver] {msg}"],
                       timeout=60, capture_output=True)
    except Exception as e:  # never die on a notification failure
        log(f"notify failed: {e}")


def load_state() -> dict:
    try:
        return json.loads(STATE.read_text())
    except Exception:
        return {"cooldown_until": {}, "sessions": 0, "idle_streak": 0, "last_model": None}


def save_state(st: dict) -> None:
    STATE.write_text(json.dumps(st, indent=2))


def proxy_key() -> str:
    for line in (Path.home() / ".hermes/.env").read_text().splitlines():
        if line.startswith("CLIPROXY_API_KEY="):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    raise RuntimeError("CLIPROXY_API_KEY not found")


def session_env(via_proxy: bool) -> dict:
    env = dict(os.environ)
    home = str(Path.home())
    env["PATH"] = ":".join([f"{home}/.local/go/bin", f"{home}/go/bin", f"{home}/.local/node/bin",
                            f"{home}/.bun/bin", f"{home}/.local/bin", "/usr/local/bin", "/usr/bin", "/bin"])
    for k in ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDECODE"):
        env.pop(k, None)
    if via_proxy:
        env["ANTHROPIC_BASE_URL"] = PROXY
        env["ANTHROPIC_AUTH_TOKEN"] = proxy_key()
    env["CLAUDE_CODE_EFFORT_LEVEL"] = "xhigh"
    return env


def head_commit() -> str:
    return subprocess.run(["git", "-C", str(REPO), "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()


def parse_reset(text: str, model: str) -> float:
    """Best-effort reset time from a limit message; default cooldown otherwise."""
    m = re.search(r"\|(\d{10})\b", text)
    if m:
        return float(m.group(1)) + 60
    m = re.search(r"resets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)", text, re.I)
    if m:
        h, mi, ap = int(m.group(1)) % 12, int(m.group(2) or 0), m.group(3).lower()
        h += 12 if ap == "pm" else 0
        t = datetime.now(HKT).replace(hour=h, minute=mi, second=0, microsecond=0)
        if t.timestamp() <= now():
            t += timedelta(days=1)
        return t.timestamp() + 120
    return now() + DEFAULT_COOLDOWN[model]


def probe(model: str, via_proxy: bool) -> tuple[bool, str]:
    """Cheap availability check. Returns (ok, text)."""
    try:
        p = subprocess.run(["claude", "-p", "Reply with exactly: ok", "--model", model, "--max-turns", "1",
                            "--output-format", "json"], cwd=REPO, env=session_env(via_proxy),
                           capture_output=True, text=True, timeout=180)
        d = json.loads(p.stdout or "{}")
        txt = str(d.get("result", "")) + p.stderr[-500:]
        return (not d.get("is_error") and d.get("subtype") == "success"), txt
    except Exception as e:
        return False, str(e)


def pick_model(st: dict):
    """First model (in priority order) whose cooldown passed and that answers a probe."""
    for model, via_proxy, timeout_s in MODELS:
        until = st["cooldown_until"].get(model, 0)
        if until > now():
            continue
        ok, txt = probe(model, via_proxy)
        if ok:
            return model, via_proxy, timeout_s
        cd = parse_reset(txt, model) if LIMIT_RE.search(txt) else now() + 15 * 60
        st["cooldown_until"][model] = cd
        log(f"probe failed for {model}; cooldown until {datetime.fromtimestamp(cd, HKT):%H:%M} :: {txt[:200]!r}")
    save_state(st)
    return None


def run_session(model: str, via_proxy: bool, timeout_s: int, st: dict) -> dict:
    st["sessions"] += 1
    n = st["sessions"]
    LOGS.mkdir(parents=True, exist_ok=True)
    logf = LOGS / f"session-{n:04d}-{datetime.now(HKT):%Y%m%d-%H%M}-{model}.jsonl"
    primary = model == MODELS[0][0]
    prompt = KICKOFF.format(model=model, scope=SCOPE_PRIMARY if primary else SCOPE_FALLBACK)
    cmd = ["claude", "-p", prompt, "--model", model, "--effort", "xhigh",
           "--dangerously-skip-permissions", "--settings", str(REPO / "tools/agent/claude-settings.json"),
           "--output-format", "stream-json", "--verbose"]
    log(f"session {n} start model={model} log={logf.name}")
    before = head_commit()
    t0 = now()
    result: dict = {}
    with open(logf, "w") as out:
        p = subprocess.Popen(cmd, cwd=REPO, env=session_env(via_proxy), stdout=subprocess.PIPE,
                             stderr=subprocess.STDOUT, text=True, start_new_session=True)
        try:
            deadline = t0 + timeout_s
            for line in p.stdout:  # type: ignore[union-attr]
                out.write(line)
                if line.startswith('{"type":"result"'):
                    try:
                        result = json.loads(line)
                    except Exception:
                        pass
                if now() > deadline:
                    log(f"session {n} hit timeout {timeout_s}s; terminating")
                    os.killpg(p.pid, signal.SIGTERM)
                    break
            p.wait(timeout=120)
        except Exception as e:
            log(f"session {n} error: {e}")
            try:
                os.killpg(p.pid, signal.SIGKILL)
            except Exception:
                pass
    dur = now() - t0
    after = head_commit()
    text = str(result.get("result", ""))
    sr = re.search(r"SESSION_RESULT:.*", text)
    log(f"session {n} end model={model} dur={dur/60:.0f}m turns={result.get('num_turns')} "
        f"error={result.get('is_error')} commits={'yes' if after != before else 'no'} "
        f"{sr.group(0) if sr else ''}")
    return {"n": n, "dur": dur, "result": result, "text": text, "progressed": after != before,
            "limited": bool(result.get("is_error")) and bool(LIMIT_RE.search(text)) or
                       (not result and dur < 300)}


def main() -> None:
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    st = load_state()
    notify("driver started")
    while not DONE.exists():
        if PAUSE.exists():
            time.sleep(60)
            continue
        choice = pick_model(st)
        if not choice:
            wake = min(st["cooldown_until"].values() or [now() + 1800])
            wait = max(300, min(wake - now(), 3 * 3600))
            notify(f"all models unavailable; sleeping {wait/60:.0f} min")
            time.sleep(wait)
            continue
        model, via_proxy, timeout_s = choice
        if st.get("last_model") != model:
            if st.get("last_model"):
                notify(f"switching model {st['last_model']} -> {model}")
            st["last_model"] = model
        r = run_session(model, via_proxy, timeout_s, st)
        if r["limited"]:
            st["cooldown_until"][model] = parse_reset(r["text"], model)
            log(f"{model} limited; cooldown until "
                f"{datetime.fromtimestamp(st['cooldown_until'][model], HKT):%m-%d %H:%M}")
        st["idle_streak"] = 0 if r["progressed"] else st.get("idle_streak", 0) + (0 if r["limited"] else 1)
        save_state(st)
        if st["idle_streak"] >= MAX_IDLE_SESSIONS:
            notify(f"{st['idle_streak']} sessions in a row made no commits; pausing 1h. "
                   f"Logs: {LOGS}")
            st["idle_streak"] = 0
            save_state(st)
            time.sleep(3600)
        time.sleep(20)
    notify("docs/agent/DONE exists: build complete, driver exiting")


if __name__ == "__main__":
    main()
