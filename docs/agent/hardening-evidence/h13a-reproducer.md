# H13a independent reproducer

2026-10-26. Parent-retained report of named `reproducer` `ac0f5569204921feb`, same inherited `gpt-6.1-sol`. Scope: request framing headers only. Base: `6dc797bb10d8fddd4c6d2dbef06446b01407f88b`; isolated `.claude/worktrees/agent-ac0f5569204921feb`. No production edits, commits, pushes or acceptance ticks. Both heavy slots released before parent final-suite execution.

## Supplied regression

- `pts/tests/http_framing_test.go` SHA-256 `34c1e0c4816baec360d447118676fd48910e191f317f6ca4192fb0225294a40c`
- `pts/tests/http/framing.pts` SHA-256 `8505c5cb2a94ef0872f93cc41d9219a987bd7deb6ef10ef48e520e18f74a71a3`
- `pts/tests/http/framing_cases.json` SHA-256 `fdabc426e3387d1bd236c801657c72a76d1cbd24bd06c01479a54b22a090349b`

82 table cases plus seven controls per HTTP and certificate-verified HTTPS: **178 leaves**. Throwaway CA/key files are generated in test temp directories; no verification bypass. The counter endpoint observes actual handler dispatch. Assertions require one empty error with an explicit close header, parsed close state and actual EOF (not a timeout), no 100 Continue, no rejected/marker dispatch. Positive controls cover pipelining, fragmented CL/chunked input, valid 100/body/follower and exact 64 MiB declared with 100 but no body allocation. No write-half-close is used (D-163).

## Executed reproduction

Compiler and fixture builds: exit 0, empty stderr. Independent finite raw-wire PTS/Node comparison: **34 strict-policy pass /48 fail** of 82, exit 1 on unchanged base. Signed CL, equal duplicate lengths, nonfinal chunked and both CL+TE orders yield two 200 responses and dispatch delta 2 on PTS, one 400/EOF/delta 0 on Node. Header-only CL+TE+Expect emits 100 then times out on PTS; Node sends 400 and closes. The original 78-case Node run is preserved separately from the final expanded table.

Corrected Go command (child):

```sh
env GOMAXPROCS=2 PTS_REQUIRE_NODE=1 PTS_HOME="$BASE_WORKTREE" \
  go -C "$BASE_WORKTREE" test -count=1 -json -p 1 -parallel 2 -timeout 5m \
  -run TestHTTPFramingRejectsAmbiguityAndKeepsValidBoundaries ./pts/tests
```

Exit 1, 39.059 s; **76 pass /102 fail leaves** (38/51 per transport). The three failed parents are not distinct defects. All four positive controls per transport pass.

The parent independently re-executed the identical frozen Go sources against that unchanged base with `GOMAXPROCS=1 -p 1 -parallel 1 -timeout 15m` and explicit `PTS_HOME`: **76 pass /105 fail test events**, exit 1, 39.214 s, empty stderr. This is the same **76/102 leaf** result plus three failed parents. Current code passes **181 test events** (178 leaves and three parents), both release and `PTS_BUILD_PROFILE=sanitize`, empty stderr. These parent results are separate from the child's report.

## Preserved harness failure and setup limitations

Original Go run: 2 passing /176 failing leaves, exit 1. Go's `http.ReadResponse` removes `Connection: close` from its Header map into `response.Close`; the helper therefore falsely failed valid replies whose raw bytes correctly contained the header. Original source/log retained as `harness-fault-http_framing_test.go` / `red-tests.jsonl`, not counted as 176 parser defects. The corrected helper parses the original raw header using textproto and separately asserts parsed close-state agreement. EOF, dispatch, follower and interim-response assertions are unchanged. Corrected run retained separately as `red-tests-corrected.jsonl`.

Two compound shell commands were rejected by the worktree-validation tool as too complex to verify. The tool directed plain separate commands; those succeeded in the same isolated worktree. No denied command ran. Exact errors and dispositions are in the archive's `commands.json`; they are tool validation errors, not provider content refusals. The setup-only dependency symlinks are not integration candidates.

## Node oracle and limits

Node v24.21.0 is an independent reference, not the policy for unsupported transfer codings or this server's body cap. Node accepts some chains/list forms and repeated TE lists; overflowing decimals and ordinary above-64-MiB incomplete bodies differ from D-229. For `Transfer-Encoding: chunked\t`, Node returns 400/EOF after one callback, while accepting mixed case, leading SP/HTAB and trailing SP. D-229 keeps surrounding SP/HTAB valid. The parent independently replayed all 82 Node inputs and confirmed these observations. No external target, proxy exploit or complete parser parity is claimed. H13b remains open.

## Retained inputs and original outputs

[Reproducer archive](h13a-reproducer-probes.tar.gz), SHA-256 `c709e1a7c83fdae97e28b438a5801ffe1174c7aa38d9181f5920d251b7eff8b7`: 28 allowlisted members, final source, original faulty helper, corrected/original RED streams, raw wire and Node results, scripts, commands and draft/final manifests. Parent checked member types/paths and PEM private-key markers before copying. No binary, generated certificate or private key. The older draft source manifest is explicitly labeled as predating the final case expansion/helper correction.

Full parent acceptance and final gate: [H13a evidence](h13a.md). This report claims reproduction, not task acceptance.
