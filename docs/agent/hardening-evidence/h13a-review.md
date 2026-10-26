# H13a scoped independent review — READY

2026-10-26. Parent-retained report from named `reviewer` `a7d7240228806dcd3`, same inherited `gpt-6.1-sol`, resumed for the integrated tests rather than replaced. Scope: D-229 request framing headers only. **READY; no actionable correctness, race/GC-safety, JS-semantics, regression or test-quality finding in the supplied slice.** This verdict does not accept H13b, all of H13, H36 or H09's blocked/deferred scope.

## Reviewed snapshot

Isolated `.claude/worktrees/h13a-review`, detached at `ed87bd65e`, with the parent's frozen header diff and the three integrated regression files. The reviewer wrote only its own ignored probe directory, no tracked production/test changes, commits, pushes or ticks.

| Reviewed input | SHA-256 |
|---|---|
| `pts/runtime/src/http.c` | `d5985538e992e9351a79c7240df2af950ace52555979007053e2e7e2047ce41a` |
| `pts/tests/http_framing_test.go` | `34c1e0c4816baec360d447118676fd48910e191f317f6ca4192fb0225294a40c` |
| `pts/tests/http/framing.pts` | `8505c5cb2a94ef0872f93cc41d9219a987bd7deb6ef10ef48e520e18f74a71a3` |
| `pts/tests/http/framing_cases.json` | `fdabc426e3387d1bd236c801657c72a76d1cbd24bd06c01479a54b22a090349b` |

Hashes matched before/after execution and the parent's integration. Latest D-229 and README/GUIDE/SPEC/ARCHITECTURE framing paragraphs were read at parent absolute paths, read-only; policy differences and H13b boundary are explicit.

## Static conclusions

- Decimal accumulation only multiplies values at most MAX_BODY, so it cannot overflow int64; saturation still checks the remaining characters.
- Zero is a seen CL, so repeated zero/zero-padded lengths are rejected too.
- Exactly one supported TE is accepted; CL+TE is rejected before the existing `process` size/100/dispatch path.
- Request locals reset between pipelined requests; no new shared state or GC ownership.
- Reviewed all 82 table cases, single-loop fixture counter, raw literal Connection/textproto plus parsed `response.Close` correction, actual EOF/read-error checks, no100/nohandler/follower assertions, fragmented positive/negative controls and exact MAX_BODY boundary.

## Independent executions

**180/180** finite local native-server checks: 90 HTTP and 90 verified HTTPS, empty PTS stderr, actual EOF after final responses. Python TLS uses `suppress_ragged_eofs=False`, so a bare TCP EOF is not mistaken for TLS close_notify. Count checks observe actual dispatch; the tests include saturation suffixes, oversized/zero CL+TE in both orders, duplicate fields, long zero prefixes, valid/invalid pipelines, state reset, fragmented heads and exact 67,108,864 header-only 100 without body allocation. No external service is targeted.

The same 180 wires were also sent to local Node v24.21.0. Differences are retained, not forced into this server's policy: transfer-coding lists, the body cap and overflow, trailing HTAB, early callback/pipeline behavior; Node HTTPS errors often end without close_notify and some fragmented plain errors reset. Those are oracle observations, not candidate failures. D-229 documents the supported-policy differences; no general Node parser parity or proxy exploit is claimed.

Corrected integrated Go test independently passed, exit 0, empty stderr:

```sh
CORE=$(python3 -I -c 'import os; print(min(os.sched_getaffinity(0)))')
taskset -c "$CORE" env GOMAXPROCS=1 PTS_THREADS=1 PTS_REQUIRE_NODE=1 \
  PTS_HOME="$REVIEW_WORKTREE" go -C "$REVIEW_WORKTREE" test \
  -count=1 -p 1 -parallel 1 -timeout 10m \
  -run '^TestHTTPFramingRejectsAmbiguityAndKeepsValidBoundaries$' ./pts/tests/
# ok github.com/microsoft/typescript-go/pts/tests 3.058s
```

The parent separately replayed the 180 custom checks: **180/180**, no mismatch, unchanged runtime hash, empty stderr. Parent release/sanitizer 181-event results and vet are parent executions, not relabeled reviewer executions. The reviewer's heavy slot was explicitly released before the exclusive parent full-suite run; no child jobs remained.

## Setup limitation retained

The initial static-only reviewer worktree had no edits and was auto-cleaned. On resumption the parent prepared the dedicated frozen candidate above. An attempted EnterWorktree returned:

> Cannot enter worktree: the current working directory /home/bryan/projects/parallelscript is the repository root, not an isolated worktree — switching is only available to sessions whose working directory is inside a worktree of this repository.

No retry or permission change. The reviewer used the already authorized absolute isolated-candidate paths, never wrote the parent's tree, and retained this as a setup limitation, not a provider content refusal. H36's separately refused review was not retried/rephrased/reassigned.

## Runnable evidence

[Credential-free probe archive](h13a-review-probes.tar.gz), SHA-256 `db08953c98bf331026d04bb4ac88dba56c9446497d045ce306a36ca100e63c85`. Eight regular files: native/Node fixtures, bounded runner, replay script, both raw result JSONs and empty stderr files. Parent checked paths, member types and private-key markers before retaining it. No binary/certificate/key; replay regenerates local throwaway credentials. Integrated-test stdout/stderr are retained separately in the parent integration archive.

[Parent final gate and acceptance](h13a.md). **READY is scoped review, not a claim that the full suite or push has already completed.**
