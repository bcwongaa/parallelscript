# H09b5b resumed checkpoint: implementation verified separately; independent acceptance blocked

Date: 2026-10-21. Parent: gpt-6.1-sol. This is a checkpoint, **not a completed slice or an independent acceptance report**. H09b5b remains unchecked; H09b5c has not started. See [original evidence](h09b5b.md) and [historical review rounds](h09b5b-review.md).

## Independent-review blocker

The required named `reviewer` (`a8254885b0bf1c71e`, isolated worktree) invoked `/code-review`; the provider content-refused that invocation. Exact error:

```text
API Error: 400 This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request. If you’re doing authorized security work that requires more cyber permissive safeguards, apply for Daybreak access via https://platform.openai.com/settings/organization/status-and-access before retrying.
```

The reviewer confirmed that this was a content refusal, not a transport failure. They stopped the refused scope. Their subsequent actions were read-only bootstrap/document/status reads; they did not perform a direct source review, build, test, production edit, or write another probe after the refusal. No independent acceptance verdict was returned.

The reviewer’s helper (`a8303c020bb2c85b0`) also stopped, with no jobs left running. Already-completed partial observations retain their original provenance: a deterministic outer-cut graph RED and a descriptor-cascade checkpoint RED. Their planned Node and reduced-demand controls were not executed before they stopped. The parent later independently ran the descriptor regression and Node comparison; that is implementation verification, not a replacement independent review.

**Disposition:** blocked independent-review gate. Do not retry, rephrase, switch models, replace the refused review, or call parent regression testing independent acceptance. The parent continued only the separately authorized implementation/regression work. No provider/owner disposition unblocking review has been received. Do not tick H09b5b or silently advance to H09b5c.

## Reproducer and parent verification

The separately assigned `reproducer` (`ab5a39deedb854bdc`, isolated worktree) completed its audit. Its [retained report](h09b5b-resume-reproducer.md) and [archive](h09b5b-resume-reproducer-probes.tar.gz) keep child observations distinct from parent verification. Archive SHA256: `5c2036970f6e15c32504bb6ba87948ab014d61f7dacabd5067a0dc02b37a7c75`; 190 members, 133,806 bytes. Parent validated paths, member types and absence of ELF binaries before extracting into a new empty scratch directory.

The parent reproduced four wrong-output cases: a single source call in generic function instances, in generic class instances, in a relaxed-cast case, and a single generic method declaration in different enclosing class instances. The checkpoint loses the working number/Good instance alongside a failing Dog[]/Base instance. These are remaining C176 defects, not regressions introduced by the checkpoint. Four active regressions now match their original expected output and Node:

- `run/generic_fallback_spares_other_instances_at_one_call.pts`
- `run/generic_fallback_spares_other_class_instances_at_one_call.pts`
- `run/generic_fallback_spares_other_cast_instances_at_one_call.pts`
- `run/generic_fallback_spares_other_method_owners.pts`

### Identity and sound cuts

Pairings now distinguish a demand’s instantiated checker type arguments, parameters and result, and the target’s enclosing generic-class parameter bindings, including erased parameters. The checker survives relowerings of one checked program; IR identities do not. These are narrowing keys, not persistent or runtime instance keys.

The parent retained a deterministic five-node graph test and a fixed-seed 10,000-graph test against an independent small-graph least-fixed-point oracle. A cached partial user result used as an `outer` alternative can propose a cut that does not remove the reported code. `blameOf` now validates a proposed substitution cut with a linear forward-worklist fixed point; an unproved cut is program-owned/conservative fallback. Validation happens before `relaxedCastUse` can convert the result into pairings and lose graph provenance. This is a reproduced graph-contract defect; no complete language-level temporal construction or native-crash claim was established.

### Descriptor reuse: RED, incomplete fix, GREEN

`run/generic_fallback_blames_descriptor_reuses.pts` has ten distinct demands that share one descriptor signature. Before the fix, parent execution and the Node differential both fail: the interface and value victims throw `Expected Box<number>, got Box<any>`, and the dogs calls do not complete. Node prints `interface victim 5`, `value victim 6`, `weird 1`, then `dogs0 1` through `dogs9 1`.

Recording every descriptor hit alone was insufficient. The first implementation still failed both runtime tests and clang smoke; the test was not weakened or removed. An observation-only overlay showed the reused function gaining all its users while the deferred dependency’s report was blamed on one pairing per relowering, exhausting the eight-lowering cap. The original trial had credited dependencies directly to its first pairing instead of to the shared function instance.

The fix records a trial’s primary function substitution (`trial.root`), makes its signature/body dependencies follow their enclosing instance, and records later descriptor hits as users of that instance (`useListedInstance`, `useFrom`). Reports still belong to the trial, as before. `specializeValue` and `specializeMethod` both record non-erased descriptor hits. Unit tests cover signature/body dependencies, distinct and duplicate users, declared/program-owned users, and nil-function entries that retain an erased copy.

`run/generic_fallback_blames_method_descriptor_reuses.pts` is an active method analogue/control. **It passed before the descriptor fix**; despite the scratch log name `method-descriptor-red`, that run exited 0 and is not RED evidence.

## Verification

Checkpoint CLI: code of `9132cb9d1` / docs checkpoint `c69034445`, SHA256 `5511438ee472007e993d5d60df192e900c0de2ec9fe1319d4efce6bfd221ef82`. The current CLI was rebuilt with all checkpoint source changes before replay. `PTS_HOME` selected the main initialized runtime for both; Node twins use `.mts` (strict ESM).

- Fresh resumed baseline: build exit 0; required four-job suite **2,101 pass / 336 skip / 0 fail**, corpus **263 / 37**, empty stderr. [Baseline stream](h09b5b-resume-baseline.jsonl.gz). This certifies the old checkpoint, not these source changes.
- Focused final regressions, Node, driver and clang smoke: **77 pass / 2 skip / 0 fail**, exit 0. Command below; `descriptor-green2.*` in the resumed archive. An earlier incomplete attempt exited 1 (72 pass / 5 fail / 2 skip); retain it as such.
- Final `make build` exit 0; affected lower/driver packages **100 pass / 1 skip / 0 fail**, exit 0 (`build-final.log`, `affected.*`).
- Parent replay of **48 content-deduplicated retained probes** against checkpoint/current/Node: runner exit 0; **32 matched Node on both**, **7 changed from nonmatching to matching**, **9 remained byte-for-byte unchanged and nonmatching**. No checkpoint Node match was lost. This is a bounded replay, not a claim of general compatibility or review acceptance.
- Fresh full current-code PTS/CLI suite: **2,122 pass / 336 skip / 0 fail**, exit 0; ten tested packages, corpus **263 pass / 37 skip**, empty stderr. The actual task completion, exit marker and full event stream were checked. [Full event stream](h09b5b-resume-final-suite.jsonl.gz); original and independently counted summaries are in the parent archive’s `suite/`. No compiler/runtime/test source edits during the run.
- CI: pending for new checkpoints; not polled or waited for. Historical exact-SHA receipts remain in [h09b5b-ci.json](h09b5b-ci.json).

The seven improved replay probes are the four identity cases plus `total_own_demand_context`, `total_trial_own_instance`, and `value_pairing_inside_by_name_trial`. The last is C251’s historical probe; its improvement does **not** establish that containing-code level caps are implemented. C251 remains assigned to H09b5c.

The nine unchanged nonmatches are `shared_class_failure_stands` (honest own PTS1001), `relaxed_cast_subclass_by_trial` (own relaxed-cast PTS1001), `total_own_demand_context2` (D-198’s total PTS1001), `kept_classes_local`, `speculative_roots_local_arrows` (existing erased-copy boundaries), `literal_method_class_body` (the failing pairing’s own call), `site_inside_trial` (C250), `union_key_site` (the failing site’s own call), and `cascade_class_body_20_padded` (same stdout and ReferenceError class on both; exact Node stderr differs in formatting/stack). Exact commands, exits and outputs are retained rather than counted as passes against Node.

## Retained artifacts and checkpoint disposition

- [Parent RED/GREEN/replay archive](h09b5b-resume-parent-probes.tar.gz): SHA256 `da856c1b28ce522bc267a62d2e46f5f9b29001a19b6aad441ec60f3b55883346`, 213 members, 172,401 bytes. `INDEX.txt` distinguishes every intermediate failure and passing control; `SHA256SUMS.json` hashes the other members. Includes original completed helper receipts/sources without restarting the refused review, the observation overlay, all 48 replay sources/results/strict-ESM twins, final changed source bytes and tracked patch against `12ab67731`, and the full-suite summaries/provenance. No binaries or credentials.
- [Final full-suite stream](h09b5b-resume-final-suite.jsonl.gz): SHA256 `512b5889ed0816f7d295af9f70a54cb13f5305a14410ea69c50270f824721a36`, 284,681 bytes. Decompressed stream SHA256 `d64e2c383126dfaf3f37e8c14c174e61e6c2ffeabeda287d4dbf23b8d596c583`. Final CLI SHA256 `e364f5f4e2ceb065bd31c8c4ddc07f7703b99cedefb9363fd69c9def8e0ab3b9`; the archived `suite/verified-summary.json` hashes all eleven changed/new compiler/test source files and records submodules.
- The authorized material-blocker Telegram status was sent successfully (actual send exit 0; `parent/blocker-message*`). It accurately reported the full suite as running at send time; the later successful completion is recorded above, not retroactively attributed to that message.

This green implementation/evidence checkpoint does not complete H09b5b. Leave the acceptance box unchecked, CI pending, and end the session without starting H09b5c. The independent-review gate remains blocked by the exact refusal above.

## Commands

```sh
GOMAXPROCS=1 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 1 -parallel 1 -timeout 5m -run 'TestRun(MatchesNode)?/generic_fallback_blames_descriptor_reuses$' ./pts/tests/
GOMAXPROCS=2 PTS_REQUIRE_NODE=1 PTS_SMOKE_FILTER=generic_fallback_ PTS_SMOKE_MUTANTS=0 PTS_SMOKE_CLANG=1 go test -count=1 -json -p 2 -parallel 2 -timeout 10m -run 'TestDescriptor|TestADeclaredDescriptor|TestAnErasedDescriptor|TestBlameOf|TestRun(MatchesNode)?/generic_fallback_|TestEmitCPrintsTheCThatBuildCompiles|TestAValueWhoseClassInstanceDoesntLowerCostsNoOtherValueItsInstances|TestCompilerFailsCleanly' ./pts/lower/ ./pts/driver/ ./pts/tests/
GOMAXPROCS=2 make build
GOMAXPROCS=2 go test -count=1 -json -p 2 -parallel 2 -timeout 10m ./pts/lower/ ./pts/driver/
GOMAXPROCS=4 PTS_REQUIRE_NODE=1 go test -count=1 -json -p 4 -parallel 4 -timeout 40m ./pts/... ./cmd/pts/
```

Scratch provenance: `/tmp/h09b5b-resume.QJm71a`; retained replay runner uses two workers, `GOMAXPROCS=2` each, process-group timeouts and immediate per-case binary deletion. It finished before the four-job full suite began. No compiler/runtime/test edits during that full suite.

## Open scope

- Independent acceptance is blocked by the exact provider refusal above.
- C248/C250 remain H17’s; C250’s test stays skipped, with its original expectation.
- C251 remains H09b5c’s, despite the incidental replay improvement.
- Site keys still use source call + kind + level, without per-instance identity. A possible additional collision lead was not executed; do not label it verified or fixed.
- No new flow-pairing, same-IR-type or dynamic-variant-cap work; those are H09b5c/H09b5d.
