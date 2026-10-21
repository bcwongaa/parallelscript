# Scoped independent review — H09b5c2b2b1

Date: 2026-10-22. Reviewer: named `reviewer`, agent `a287d8e55867393f9`, isolated worktree on `d56b514c9`, inherited `gpt-6.1-sol`. Parent independently checked findings and final production byte equality. This is **only** exact constructed receiver dispatch, not acceptance or a replacement review of H09b5b's refused scope, not completion of any parent, and not captured-wrapper/container flow b2.

## Verdict

**READY after C257's fix; no outstanding verified defect in the scoped final production snapshot.** This is the review verdict, not a claim that review ran the parent's required full suite. The parent's fresh frozen full suite and final commit status are recorded in [slice evidence](h09b5c2b2b1.md).

All child heavy execution was serial (`GOMAXPROCS=1`, `-p 1`, `-parallel 1`); no child full suite, CI calls, nested agents, protected-file edits, production fixes, task ticks, commits or pushes. The reviewer released their heavy slot before the parent's final four-job suite. The reproducer had already finished/released theirs.

## Verified C257, corrected by the parent

The initial shared declaration-write index called `ast.IsAssignmentTarget(identifier)`. That helper does not climb assertion expressions. Therefore these tsc-valid forms left the constructor incorrectly proven exact:

```ts
class A {}
class B {}
(A as any) = B;
const probe = new A();
probe;
```

The other independently failing forms were `(<any>A) = B` and `[(A as any)] = [B]`. Round1 source-only RED returned `{A}` instead of UNKNOWN: three subtest failures plus the parent test failure. Parent independently reproduced all three **without ts-ignore**, before fixing production.

The bounded `sourceBindingWritten` walk now climbs parentheses, non-null, as/type assertions and satisfies wrappers before testing the outer assignment target. Exhaustion conservatively marks the binding written. The reviewer copied only the final changed production/test files and independently verified all 16 class-write forms GREEN. Ordinary function bindings share this write index; separate asserted-function-write guards pass too.

Classification matters: these assignments typecheck, but **both exact-base and current native lowering already reject their native assignment forms with PTS1001**. This is a verified conservative-proof gap, **not an established supported native wrong-output regression**. The fix does not broaden lowering or checked-any permissions.

## Independently executed gates

Worktree (`WT`): `.claude/worktrees/agent-a287d8e55867393f9`.

```sh
GOMAXPROCS=1 PTS_HOME=/home/bryan/projects/parallelscript \
go test -count=1 -json -p 1 -parallel 1 -timeout 15m "$WT/pts/lower" "$WT/pts/driver"
```

Final production: **404 pass / 0 fail test events**, both packages PASS, empty stderr. This is the reviewer's isolated test inventory, not the parent's 389-event inventory; it includes independent source probes and predates their last additional source-only file, not any production change.

```sh
GOMAXPROCS=1 PTS_HOME=/home/bryan/projects/parallelscript \
go test -count=1 -json -p 1 -parallel 1 -timeout 10m -run '^TestReview' "$WT/pts/lower"
```

**36 pass / 0 fail events**, empty stderr: 16 class-write forms; six both-order function/receiver cache cases; three asserted-function writes; seven constructor/control-flow/ancestor boundaries; four parent test events. Constructor controls cover replacement returns in catch/finally/unreachable arms, grandparent replacement, written ancestors, bare finally returns and nested callable returns. Unknown boundaries stay unknown.

```sh
GOMAXPROCS=1 PTS_HOME=/home/bryan/projects/parallelscript \
PTS_SMOKE_MUTANTS=0 \
PTS_SMOKE_FILTER='generic_fallback_(spares_unrelated_(literal_method|forwarder)|blames_method_descriptor_reuses)' \
PTS_SMOKE_CLANG=1 PTS_SMOKE_JOBS=1 \
go test -count=1 -v -p 1 -parallel 1 -timeout 10m \
  -run '^TestCompilerFailsCleanly$' "$WT/pts/tests"
```

**3 programs / 3 emitted C, PASS**. The reviewer inspected and independently ran the fallback fixture correction: unknown `Maker[]` read restores its fallback precondition while every original assertion remains. The smoke exemption removals strengthen AllInstances requirements. The new native fixture's explicit `bind(receiver)` correction is valid for strict Node and preserves expected stdout.

## Direct-AllInstances native review matrix

The reviewer built exact-base overlay and final strict helpers, calling `driver.EmitCWith(..., lower.AllInstances)` then the toolchain without driver fallback. Serial `python3 -I review-probes/run.py review-probes` replayed **31 inputs** against exact base, round1 and final snapshots plus strict-ESM `.mts` Node twins (`--experimental-transform-types`). Final results are unchanged from round1 after C257's fix:

- **26 compiling final cases; all 26 required/excluded target checks pass.** Required checks include every named implementation, not merely a count.
- **23 native Node matches on both base and final**.
- **Two pre-existing nonmatches:** direct and inherited constructor replacement (C256 → H29).
- **One intentional checked-any difference:** overriding method with an incorrect return throws rather than falling through to an ancestor.
- **Five unchanged unsupported inputs:** constructor alias, nested class, class arrow field converted to an interface method, two asserted class-binding assignments. These are not passing Node comparisons.

The matrix covers returned callable/closed shared parameters/finally joins; optional, constant-key, runtime-key and abstract dispatched calls; ancestors and overriding; function-source coexistence; literal exclusion; and conservative captured-wrapper receivers (deferred b2).

Initial Node strip-only errors for parameter properties were corrected to `--experimental-transform-types` before conclusions. Their original failures remain archived. The packaged portable replay script itself was **not rerun after packaging**; its underlying commands and probes above were run. Parent verification must not silently turn that into a stronger reproducibility claim.

## Parent verification and archive

Before final testing, parent independently compared all five reviewed production files byte-for-byte with the parent tree: `instances.go`, `returned_values.go`, `value_flow.go`, `wrapper_sources.go`, `receiver_sources.go`.

Parent C257 RED, final affected packages (**389 pass**) and post-fix growth/native/clang replays are retained in the parent archive. After the frozen full suite completed, parent independently ran the reviewer's source tests through a Go overlay against the actual parent production: **36 pass / 0 fail / 0 skip**, exit 0. Parent's own strict-AllInstances helper and the reproducer's exact-base helper then replayed all **31** review inputs using the same copied input path and strict Node with warnings suppressed: **26 compiling final required/excluded target checks pass, 23 native Node matches on each version, the same three classified nonmatches and five unchanged unsupported inputs**. All parent replay assertions pass; stderr is empty. The 9,751-file final source manifest and CLI still match afterward. Commands, source overlay/tests, native/C/.mts outputs and final integrity receipts are in `h09b5c2b2b1-parent-probes.tar.gz` (`parent-review-replay/`, `parent-post-replay-integrity.json`). Raw reviewer round1/final receipts differ only in Node ExperimentalWarning process IDs; normalization of those IDs alone yields equality, recorded separately, not a byte-identity claim.

Archive: `h09b5c2b2b1-review-probes.tar.gz`, **155 regular files**, 158,099 bytes; SHA-256:

```text
b9033265a06caa8ea46db21471cb90bea42f3a331b38cadcb0812c59989066e0
```

Parent checked the hash and every member before copying: no absolute/parent-traversal paths, symlinks/hardlinks or ELF files. It retains exact-base/round1/final source inputs, source tests, RED/GREEN/affected/smoke/native outputs, native `.pts`/strict `.mts` twins, final hashes and strict-helper/prepare/replay source. No binaries are committed.

**CI: pending**, not polled. Only b1 can be accepted after its own local full gate and implementation push; b2/all parents remain unchecked and H09b5b's refused independent acceptance remains untouched.
