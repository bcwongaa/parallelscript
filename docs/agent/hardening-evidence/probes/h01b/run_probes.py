#!/usr/bin/env python3
"""Run H01b probe programs under ParallelScript and Node and record the outcomes.

Each probe starts with a header (see h01b.md):
    // H01b <ROW> <red|control>: <description>
    // oracle: <node-self | node:<name>.oracle.mts>
    // threads: <comma list>
    // runs: <runs per thread count>
    // expected (Node): ...
    // baseline PTS: ...
    // failure: <Python regex; red probes only>

A probe passes when PTS builds it and every run's stdout and exit code equal
the Node oracle's (stderr is retained, not compared). Probe binaries run with a
4 GiB address-space cap and a 60 s timeout. The oracle must exit 0 with some
stdout; otherwise the probe is an oracle error.

A red probe is expected to fail in the way it claims: PTS panics, gives a
diagnostic or prints something other than Node, and its `failure` regex
matches the build's stderr (for a failed build) or the stdout and stderr of
every run that differs from Node, but not the oracle's stdout. A control is expected to pass. A timeout is
never expected. The script exits 0 only when every probe behaves as its kind
says, so a fixed defect (red now passing), a red failing some other way and a
broken control are all loud.

Usage: run_probes.py --pts CLI --node NODE --scratch DIR --out JSON probe.pts...
"""

import argparse
import datetime
import hashlib
import json
import os
import re
import resource
import shutil
import subprocess
import sys
from pathlib import Path

BUILD_TIMEOUT_S = 600
RUN_TIMEOUT_S = 60
PROBE_ADDRESS_SPACE_BYTES = 4 << 30
STDERR_KEEP_LINES = 40
HEADER_RE = re.compile(r"^// H01b (C\d\d) (red|control): (.+)$")
FIELD_RE = re.compile(r"^// (oracle|threads|runs|expected \(Node\)|baseline PTS|failure): (.*)$")
REQUIRED_FIELDS = {"oracle", "threads", "runs", "expected (Node)", "baseline PTS"}
RED_FAILURES = ("build-panic", "build-error", "run-mismatch")


def parse_header(path):
    lines = path.read_text().splitlines()
    head = HEADER_RE.match(lines[0]) if lines else None
    if head is None:
        raise ValueError(f"{path}: first line is not an H01b header")
    fields = {}
    for line in lines[1:]:
        field = FIELD_RE.match(line)
        if field is None:
            break
        fields[field.group(1)] = field.group(2).strip()
    kind = head.group(2)
    required = REQUIRED_FIELDS | ({"failure"} if kind == "red" else set())
    missing = required - fields.keys()
    if missing:
        raise ValueError(f"{path}: header lacks {sorted(missing)}")
    return {
        "row": head.group(1),
        "kind": kind,
        "description": head.group(3),
        "oracle": fields["oracle"],
        "threads": [int(t) for t in fields["threads"].split(",")],
        "runs": int(fields["runs"]),
        "expected_node": fields["expected (Node)"],
        "baseline_pts": fields["baseline PTS"],
        "failure": fields.get("failure"),
    }


def cap_address_space():
    resource.setrlimit(resource.RLIMIT_AS, (PROBE_ADDRESS_SPACE_BYTES, PROBE_ADDRESS_SPACE_BYTES))


def run_command(command, timeout, env=None, capped=False):
    """Run a command and return its exit code and output; a timeout is exit 124.

    A capped command (a probe binary) gets a bounded address space, so a probe that
    allocates without end (an eagerly drained infinite iterator) fails instead of
    exhausting the shared host's memory.
    """
    try:
        done = subprocess.run(command, capture_output=True, text=True, errors="replace",
                              timeout=timeout, env=env,
                              preexec_fn=cap_address_space if capped else None)
        return {"command": command, "exit": done.returncode,
                "stdout": done.stdout, "stderr": done.stderr}
    except subprocess.TimeoutExpired as expired:
        return {"command": command, "exit": 124, "timed_out": True,
                "stdout": expired.stdout or "", "stderr": expired.stderr or ""}


def trim_stderr(result):
    lines = result["stderr"].splitlines()
    if len(lines) > STDERR_KEEP_LINES:
        result["stderr"] = "\n".join(lines[:STDERR_KEEP_LINES]) + f"\n… ({len(lines)} lines in all)"
    return result


def is_go_panic(build):
    return build["exit"] != 0 and re.search(r"^panic: ", build["stderr"], re.M) is not None


def run_oracle(probe_path, oracle, node, scratch):
    if oracle == "node-self":
        program = scratch / (probe_path.stem + ".mts")
        shutil.copyfile(probe_path, program)
    elif oracle.startswith("node:"):
        program = probe_path.parent / oracle[len("node:"):]
    else:
        raise ValueError(f"{probe_path}: unknown oracle {oracle!r}")
    return trim_stderr(run_command([node, "--no-warnings", "--experimental-transform-types",
                                    str(program)], RUN_TIMEOUT_S))


def is_healthy_oracle(oracle_result):
    return oracle_result["exit"] == 0 and oracle_result["stdout"] != ""


def run_binary_repeatedly(binary, threads, runs, oracle_result):
    """Run the binary `runs` times per thread count; group identical outcomes."""
    per_thread = []
    for thread_count in threads:
        env = dict(os.environ, PTS_THREADS=str(thread_count))
        outcomes = {}
        for _ in range(runs):
            result = run_command([str(binary)], RUN_TIMEOUT_S, env, capped=True)
            key = (result["exit"], result["stdout"], result["stderr"])
            if key not in outcomes:
                outcomes[key] = {"count": 0, "sample": trim_stderr(result)}
            outcomes[key]["count"] += 1
        groups = sorted(outcomes.values(), key=lambda group: -group["count"])
        for group in groups:
            group["matches_node"] = matches(group["sample"], oracle_result)
        mismatching = sum(g["count"] for g in groups if not g["matches_node"])
        per_thread.append({"threads": thread_count, "runs": runs,
                           "runs_not_matching_node": mismatching, "outcomes": groups})
    return per_thread


def matches(result, oracle_result):
    return result["exit"] == oracle_result["exit"] and result["stdout"] == oracle_result["stdout"]


def classify(build, per_thread, oracle_result):
    if not is_healthy_oracle(oracle_result):
        return "oracle-error"
    if build["exit"] != 0:
        return "build-panic" if is_go_panic(build) else "build-error"
    groups = [g for t in per_thread for g in t["outcomes"]]
    if any(g["sample"].get("timed_out") for g in groups):
        return "run-timeout"
    if any(not g["matches_node"] for g in groups):
        return "run-mismatch"
    return "run-match"


def failure_texts(build, per_thread):
    """The texts a red probe's failure regex must match: the failed build's stderr,
    or the stdout and stderr of each run that differs from Node."""
    if build["exit"] != 0:
        return [build["stderr"]]
    return [g["sample"]["stdout"] + g["sample"]["stderr"]
            for t in per_thread for g in t["outcomes"] if not g["matches_node"]]


def fails_as_claimed(failure, build, per_thread, oracle_result):
    """Whether the failure regex matches every failure text and not the oracle's stdout:
    a regex that correct output also matches proves nothing about the defect."""
    texts = failure_texts(build, per_thread)
    pattern = re.compile(failure, re.M)
    return (bool(texts) and all(pattern.search(text) for text in texts)
            and not pattern.search(oracle_result["stdout"]))


def probe_behaves_as_kind(header, outcome, build, per_thread, oracle_result):
    if header["kind"] == "red":
        return (outcome in RED_FAILURES
                and fails_as_claimed(header["failure"], build, per_thread, oracle_result))
    return outcome == "run-match"


def run_probe(probe_path, pts, node, scratch):
    header = parse_header(probe_path)
    binary = scratch / (probe_path.stem + "-native")
    build = trim_stderr(run_command([pts, "build", str(probe_path), "-o", str(binary)],
                                    BUILD_TIMEOUT_S))
    oracle_result = run_oracle(probe_path, header["oracle"], node, scratch)
    per_thread = []
    if build["exit"] == 0:
        per_thread = run_binary_repeatedly(binary, header["threads"], header["runs"], oracle_result)
    outcome = classify(build, per_thread, oracle_result)
    return {"probe": str(probe_path), "probe_sha256": sha256_of(probe_path), **header,
            "build": build, "node": oracle_result, "pts_runs": per_thread, "outcome": outcome,
            "as_expected": probe_behaves_as_kind(header, outcome, build, per_thread, oracle_result)}


def tool_versions(node):
    def first_line(command):
        return run_command(command, 60)["stdout"].splitlines()[0]
    return {"go": first_line(["go", "version"]), "node": first_line([node, "--version"]),
            "clang": first_line(["clang", "--version"])}


def sha256_of(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--pts", required=True)
    parser.add_argument("--node", required=True)
    parser.add_argument("--scratch", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("probes", nargs="+", type=Path)
    args = parser.parse_args()

    args.scratch.mkdir(parents=True, exist_ok=True)
    source_sha = run_command(["git", "rev-parse", "HEAD"], 60)["stdout"].strip()
    results = [run_probe(p, args.pts, args.node, args.scratch) for p in sorted(args.probes)]
    record = {
        "task": "H01b",
        "date": datetime.date.today().isoformat(),
        "source_sha": source_sha,
        "cli": args.pts,
        "cli_sha256": sha256_of(args.pts),
        "tools": tool_versions(args.node),
        "comparison_contract": "stdout and exit code; stderr retained, not compared",
        "results": results,
    }
    args.out.write_text(json.dumps(record, indent=1) + "\n")

    for result in results:
        mismatch = ", ".join(f"{t['runs_not_matching_node']}/{t['runs']}@{t['threads']}t"
                             for t in result["pts_runs"])
        print(f"{'ok ' if result['as_expected'] else 'BAD'} {result['row']} {result['kind']:7} "
              f"{result['outcome']:13} {mismatch:24} {Path(result['probe']).name}")
    unexpected = [r for r in results if not r["as_expected"]]
    print(f"{len(results)} probes, {len(unexpected)} not behaving as their kind says")
    return 1 if unexpected else 0


if __name__ == "__main__":
    sys.exit(main())
