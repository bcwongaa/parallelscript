#!/usr/bin/env python3
# Run every .pts probe with pts, and the ones marked compare-node with Node too.
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path("/home/bryan/projects/pts-review-grok")
PTS = ROOT / "pts-bin" / "pts"
NODE = Path(os.environ.get("NODE", str(Path.home() / ".local/node/bin/node")))
PROBES = Path(__file__).resolve().parent
OUT = Path("/tmp/pts-review-grok/probe-bins")
ENV = os.environ.copy()
ENV.update({
    "GOMAXPROCS": "2",
    "PTS_CACHE_DIR": str(Path.home() / ".cache/pts-review-grok"),
    "PTS_HOME": str(ROOT),
    "PTS_THREADS": "4",
})


def run(cmd, timeout, env=None):
    try:
        proc = subprocess.run(
            cmd,
            cwd=ROOT,
            env=env or ENV,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=timeout,
        )
        return proc.returncode, proc.stdout
    except subprocess.TimeoutExpired as exc:
        out = exc.stdout or ""
        if isinstance(out, bytes):
            out = out.decode(errors="replace")
        return "timeout", out


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    files = sorted(PROBES.glob("*.pts"))
    summary = []
    for src in files:
        print(f"\n===== {src.name} =====", flush=True)
        binary = OUT / src.stem
        code, output = run([str(PTS), "build", str(src), "-o", str(binary)], 120)
        if code != 0:
            print(output)
            print(f"COMPILE {code}")
            summary.append((src.name, f"compile {code}"))
            continue
        code, output = run([str(binary)], 20)
        print(output, end="" if output.endswith("\n") or output == "" else "\n")
        print(f"RUN {code}")
        node_note = ""
        text = src.read_text()
        if text.startswith("// compare-node"):
            with tempfile.TemporaryDirectory() as tmp:
                twin = Path(tmp) / (src.stem + ".ts")
                twin.write_text(text)
                ncode, nout = run(
                    [str(NODE), "--experimental-strip-types", str(twin)],
                    20,
                    env=os.environ.copy(),
                )
            same = nout == output
            node_note = " node=same" if same else " node=DIFF"
            if not same:
                print("--- node ---")
                print(nout, end="" if nout.endswith("\n") or nout == "" else "\n")
                print(f"NODE {ncode}")
        summary.append((src.name, f"run {code}{node_note}"))
    print("\n===== summary =====")
    for name, status in summary:
        print(f"{status:28} {name}")


if __name__ == "__main__":
    main()
