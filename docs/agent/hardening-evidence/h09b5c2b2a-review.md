# H09b5c2b2a — scoped independent wrapper-provenance review

Date: 2026-10-22. Named reviewer `a473cbb9d9e302831`, isolated worktree, inherited/surfaced `gpt-6.1-sol`, configured/requested xhigh. No model/provider switching. Production read-only; probe/overlay sources isolated. Review covers **only the new D-205 exact wrapper and captured-expression slice**, never H09b5b's refused review or acceptance.

## Final scoped verdict

> No new correctness, JS-semantics, race/GC-safety, or regression finding verified in H09b5c2b2a. The supplied production patch is suitable for parent validation, subject to the limitations below.

The six reviewed production files and both new table files were byte-identical between reviewed overlay and parent checkout (`final-source-checks.json`). This is a scoped no-new-finding verdict, not an independent full-suite certification or global correctness proof. Broader receiver/container/call-return inference remains outside the slice.

The reviewer checked actual-registration versus static guesses, one source site representing every closure/receiver/capture, checker-contextual generic ancestors, unknown caching/budgets, inner captured AST versus outer caller, named/unknown-source bypasses, and trial rollback/re-registration state. Runtime code is unchanged.

## Reviewer execution

- All **seven new focused wrapper/table groups** passed, including required/excluded specialization pairs, unused wrapper descriptors, signature/capture sharing, conservative interface receivers, unknown origins and shared-generic code.
- Four `TestReview*` guard groups passed. Source includes specialization and erased-substitution gates, cached unknown before registration, depth/parenthesis/zero-work bounds, contextual-generic ancestors, sticky token after `takeBack` and re-registration, named/unknown-origin bypasses. Command:

  ```sh
  GOMAXPROCS=1 go test -p 1 -parallel 1 -count=1 \
    -overlay <reviewer>/overlay-review-r1.json -run '^TestReview' ./pts/lower/
  ```

- Direct `EmitCWith(AllInstances)` → clang → native, **without driver fallback**, compared against strict `.mts` Node: base/current match for `shared_bound_receivers`, `shared_object_captures`, `mutable_snapshot_bind`, `captured_bind_chains`.
- At 256 wrapper-source sites, fix excludes unrelated `unused_C`; at 257 both versions retain it when the bounded proof becomes unknown. Required `first_C` remains in all four outputs.
- Combined generic-context case passes generic class/syntactic-generic portions, then fails identically on exact base/current at the contextual generic arrow returning bind. The minimized eight-line case is C255 below, not a new regression.
- `lower_test.go` golden change swaps two intact function blocks; bodies, signatures and captures unchanged. Final extra parent guards and the checked-unknown fixture amendment were inspected.

## Verified pre-existing divergence: C255 → H29

Minimal `cases/contextual_returned_bind_min.pts`:

```ts
class Box<T> { constructor(public value: T) {} }
type Factory = <T>(value: T) => Box<T>;
const contextual: <U>(outer: U) => Factory = outer => {
  const arrow = <T>(value: T): Box<T> => { console.log(outer); return new Box(value); };
  return arrow.bind(undefined);
};
function use(f: Factory): void { const result: Box<number> = f(7); console.log(result.value); }
use(contextual(5));
```

Node exit 0: `5\n7\n`. Both exact-base/current direct-AllInstances natives exit 1 after `5\n`, with `Expected Box<number>, got Box<any>` at the typed call. Root cause not established. Parent independently rebuilt/replayed this and the combined case, then added skipped `run/generic_contextual_returned_bind.pts`; original expected output is retained. H29 remains open, no intentional-semantics ruling.

For C254 (inline asserted-any nongeneric adaptation), the reviewer **inspected the parent's base/current receipts**, not a separate reviewer execution. The reviewed amendment preserves the original failing guard and records the limitation, while testing a supported explicit checked `Factory` binding without changing expected stdout or unknown-pair exclusions. Parent seven-case direct-AllInstances/default/Node replay is separate evidence in the [main report](h09b5c2b2a.md).

## Smoke-gate addendum

Parent's generic clang gate found the unchanged `generic_fallback_spares_unrelated_adapter.pts` no longer needs narrowing. Reviewer inspected removal of **only that exemption** plus explanatory comment; the seed is byte-identical to base. `fallbackFailure` now requires AllInstances success, strengthening coverage. No additional finding; no reviewer heavy command for this addendum. Parent direct base/current/Node replay and final **184 programs / 142 C** smoke PASS establish the actual behavior.

## Parent independent replay and limitations

Parent read the guard/helper/runner sources and rebuilt strict base/current helpers itself, using exact `3c021e223` production overlay for base and current parent production for fix. Initialized runtime supplied with `PTS_HOME=/home/bryan/projects/parallelscript`; no private child runtime substitution or fallback. Parent guard-only overlay: **15 pass events / 0 fail**, all four groups. Four receiver/capture/mutation/bind controls match Node on both compilers. Both contextual-generic observations retain identical pre-existing failure. Adapter seed gains one AllInstances native match. All four 256/257 boundary assertions agree with review; overflow generated C byte-identical.

No reviewer full suite, race detector, dedicated GC stress or native failed-trial/rollback scenario was run. The rollback check is source-level state coverage. Parent's fresh frozen required four-job suite is recorded separately in the main report; it is not attributed to the reviewer.

Both child heavy slots were released before that final suite, with no further child execution during it. No reviewer production/task-checkbox/commit/push edits.

## Evidence and scope correction

Archive `h09b5c2b2a-review-probes.tar.gz`: **70 regular files and 25 directory entries**, no symlinks/hardlinks/unsafe paths or retained binaries. SHA-256 `7c6197eff308a4f311f02d1d0ba5adfd5d1be8b3b62ba698e67a0a365a5ac6ac`. Parent independently validated digest/member types and replayed live authored helper sources; no script from an extracted archive was run. Contains probe/helper/overlay sources, raw guard/native/boundary outputs and generated C; report saved here by the parent from delivered messages. Absolute scratch paths in receipts are historical, not portable commands without rebasing.

Earlier nested review-skill invocation before IMPLEMENTED incorrectly defaulted to historical documentation in its clean worktree. Parent stopped that scope; its empty result explicitly had **no implementation/acceptance verdict** and is excluded from evidence. Actual verdict above is the named reviewer's later manual inspection of the explicit new-slice patch, plus probes and follow-ups. No provider refusal occurred for this new slice; the blocked H09b5b refusal was not retried or replaced.

Parent final validation is complete: fresh build and exact required four-job full suite exit 0, **2,356 pass / 339 skip / 0 fail**, all ten tested packages PASS; corpus **263 / 37**, empty stderr. All **6,675 source hashes** and the final CLI remained unchanged. Implementation push/task acceptance are separate parent actions recorded in the main report. Only c2b2a can be accepted; c2b2b/all parents and H09b5b's refused independent acceptance remain open.
