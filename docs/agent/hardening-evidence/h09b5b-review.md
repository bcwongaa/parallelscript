# H09b5b review reports

The `reviewer` subagent's reports (claude-opus-5-5, effort xhigh), saved verbatim by the parent, one section per round. Each round reviewed a byte-checked snapshot of the parent's change; the parent's responses are in [h09b5b.md](h09b5b.md).

## Round 1 (snapshot `snap-a`, the fix commit `3dde86f42`)

Archive: [h09b5b-review1-probes.tar.gz](h09b5b-review1-probes.tar.gz) (sha256 `cb1d22b37ca4ab36…`, 526 entries; `INDEX.txt`).

---

I tested the slice on `/tmp/h09b5b/snap-a`, which matches commit `3dde86f42` byte for byte. Narrowing helps the cases it was built for, but my verdict is **not ready**: four probes print Node's output on base and throw a TypeError with the fix.

Your working tree has changed since the snapshot (`docs/SPEC.md`, `narrow_fallback_test.go`, `pts/lower/classes.go`), and I didn't review those edits. The archive (526 entries, sha256 `cb1d22b3…`) is `/tmp/h09b5b/review1/h09b5b-review1-probes.tar.gz`; repro paths below are inside it.

## Findings, by severity

### F1 (high): a pairing ValueInstances keeps is left out at AllInstances for a failure only AllInstances causes
- **Repro `p/relaxed_cast_source_by_name.pts`:**
  - Base: `value call 2 5`.
  - Fix: `Uncaught TypeError: Expected Box<HasId>, got Box<any>`.
  - Node: `value call 2 5`.
  - How it happens: a relaxed cast in a local arrow's instance fails only because an interface-by-name instance that never runs (`CM.make<Derived>`) converts a `Derived` to `HasId`.
- **Repro `p/total_spent_by_name_2.pts` (and `_3`):**
  - Base: `value call 6`.
  - Fix: `Uncaught TypeError: Expected Box<number>, got Box<any>`.
  - Node: `value call 6`.
  - How it happens: by-name trials of recursive interface methods spend Box's total, then the local arrow's value pairing hits it.
- **Root cause:** `trial.narrowable` means "DeclaredInstances doesn't make it" at every level (`instances.go:807`, `:1034`, `narrowing.go:171`). At AllInstances, a ValueInstances pairing is therefore left out. Base fell back to ValueInstances, where that pairing compiles. The blame is wrong in both cases:
  - A relaxed cast is blamed only on its maker (`optional.go`), not on the instance that supplied the conflicting source.
  - A total is blamed on whichever trial happens to cross it (`instances.go:893`).
- **Suggested fix:** decide narrowability per level.
  - At AllInstances, blame only what ValueInstances doesn't make: demands by name, forwarders', adapters', object-literal methods, and `keyedInstanceCall` sites.
  - Below AllInstances, keep the current rule.
  - A prototype is in `proto/` (`proto-vs-snapshot.patch`). It fixes F1–F3's probes, keeps the expected outputs of the three slice run tests, `total_cliff_interface` and the literal-method case, and the driver tests pass under it.

### F2 (high): blame lands on an unrelated trial
- **Repro `p/leftover_body_misblame.pts`:**
  - Base: `victim 6`.
  - Fix: `victim threw Expected Box<number>, got Box<any>`.
  - Node: `victim 6`.
- **What happens** (traced with `PTS_NARROW_TRACE`):
  1. A by-name trial doesn't fit, so `lowerPending` is skipped (`instances.go:895`).
  2. The erased copy of `Holder<Dog[]>.make` it declared stays queued; `instances.go:915` only removes `t.instances`.
  3. The next trial, the victim's `{named(5), named}`, runs `lowerPending` and lowers that leftover.
  4. `Weird<Dog[]>` then records `trial: l.trial` (`generic_classes.go:184`; also `generics.go:979`, `:1182`).
  5. `blameHere` (`narrowing.go:92`) returns the first narrowable trial on the chain, which is the victim's.
- `p/class_instance_method_trial.pts` shows the same misblame: lowering 1 leaves out `{named(5), named}`.
- **Suggested fix:**
  - Record `trial` only when that trial owns the code being lowered (`t.making > 0 || t.owns(l.function)`); otherwise rely on the `made` chain.
  - Better: lower leftover pending bodies outside the next trial, or drop them with the failed trial.

### F3 (medium-high): narrowing carries to the next level, and a left-out constant-key site doesn't lower like the last fallback
- **Repro `p/constant_key_site_carried.pts`** (the `call_site` trigger written as `m["f"](maybe(), 1, 2)`):
  - Base compiles at ValueInstances: `victim 6`, same as Node.
  - The fix ends at DeclaredInstances: `victim threw Expected Box<number>, got Box<any>`.
- **What happens:**
  1. AllInstances blames the site (`keyedInstanceCall` and `constantKeyCall` share the key `{call: node}`).
  2. Lowered again with the site left out, the call takes the value-call path: a rest-parameter demand that fails with its own error.
  3. ValueInstances is lowered with the site still left out (`driver.go:102`), so it fails the same way.
- **Forced sweep:** with every site left out (`out/sweep.log`), `Statics[LATE]("ab")` in `generic_element_access_key_reads.pts` becomes "the class `Statics` as a value is not supported yet", where ValueInstances compiles.
- **Docs:** SPEC §9.137 says a left-out constant-key site lowers "as the last fallback does". It doesn't.
- **Suggested fix:**
  - Start each level with no narrowing (the prototype does this).
  - Give by-name sites and constant-key sites separate keys.
  - Make a left-out constant-key call make no demand, as DeclaredInstances' `demandAt` does.

### F4 (medium): cascades, one pairing per lowering, capped at 8 per level
- When N demands pair with a value whose class-instance body fails, only the trial that first made the class instance is blamed. Each lowering therefore leaves out one pairing.
- `p/cascade_class_body_10_victims.pts`: the cap is used up at AllInstances, the build falls back to ValueInstances, and the interface victim throws (Node prints 5). C176 still holds for 9 or more demands.
- `p/cascade_class_body_20_victims.pts`: 17 lowerings, ending at DeclaredInstances, same as base.
- Padded to 9,162 lines, emit-c takes 4.26 s against base's 0.90 s.
- Off by one: when the cap is hit, `lowerNarrowing` returns a narrower that was never lowered (`driver.go:85`), which contradicts its doc comment.
- **Suggested fix:** blame a class body's failure on every trial that made or looked up that class instance.

### F5 (low-medium): cost
- Narrowed AllInstances keeps speculative trees up to the totals:
  - `total_cliff_interface`: 61,619 lines of C against 839, and a 7.9 s build against 0.8 s (the output is now correct).
  - `p/speculative_roots_local_arrows.pts`: 12.2 s against 1.5 s, and it is still a TypeError.
- emit-c's clang check adds about 0.06–0.1 s per program.

### F6 (low): link failures no longer fall back
- I find this acceptable, but it needs to be logged in D-199. The ARCHITECTURE paragraph doesn't mention it.

### F7 (low, already on base): a failing demand by name inside a trial's instance
- `p/site_inside_trial.pts`: the trial is taken back silently, so the value loses its instance. Base and fix both throw a TypeError; Node prints `value call 6`.
- `p/keyed_call_site.pts` and `p/union_key_site.pts`: the left-out path fails at both AllInstances and ValueInstances, so C176 isn't narrowed there (no regression).

### Docs, comments and tests
- D-199 is cited in a test, SPEC and ARCHITECTURE but isn't in `DECISIONS.md`. It also needs to supersede D-198's sentence "past a total … stands … falls back as the base's did".
- Small comment errors:
  - `blameReport`'s comment mentions a `d` that isn't a parameter.
  - The `site` field's comment says `(atSite)`.
  - The `Narrowing` doc says left-out sites lower as ValueInstances does.
- Test gaps: no committed test for a pairing only AllInstances makes being narrowed, for the narrowing carried to the next level, for the cap, or for the left-out constant-key path. The committed run tests only cover ValueInstances pairings narrowed at AllInstances, which is the behaviour behind F1.

## Checked and fine
- **Determinism and emit-c:** emit-c gives the same output on repeated runs. Base and fix emit-c are identical on 120 sampled run tests (the one difference is expected) and on 74 probes from D-198's review rounds (only `total_cliff_interface` differs).
- **Narrowing cases:** the object-literal method pairing is narrowed correctly; `kept_classes_local` gives the same result on base and fix.
- **Slice tests:** the three run tests and the errors guard pass.
- **Blame bookkeeping:** I found no aliasing bug in how blames are saved and restored.
- **C check:** clang's `-w` keeps default-error diagnostics (`cc/t1.c`). With a missing C compiler or PTS home, emit-c still prints the C.

**Verdict: not ready.** Fix F1–F3 before ticking the task; F4 should come with them.

The debug and prototype CLIs aren't in the archive; they are at `/tmp/h09b5b/review1/pts-debug` and `/tmp/h09b5b/review1/pts-proto`, and `INDEX.txt` has the rebuild commands.

## Round 2 (snapshot `snap-d`, `578b12d9f`)

Archive: [h09b5b-review2-probes.tar.gz](h09b5b-review2-probes.tar.gz) (sha256 `0b898e65e9af8f24…`, 380 entries; `INDEX.txt`).

---

**Verdict: not ready.** Round 1's findings F1–F4 and F7 are fixed: none of the 20 round-1 probes prints worse than base, and emit-c for ordinary programs is unchanged. But three new probes (R2-1, R2-2, R2-4 below) print Node's output on base and a TypeError on snap-d. All three come from one change: a call site's report inside a trial now stands (`atSites`), and the rest of that statement counts as the site's code (`statementSites`).

On your note: removing `tryInstance`'s `lowerPending()` calls does matter (finding WT). For removing `t.making++`, I found no case where it changes anything.

Everything is in `/tmp/h09b5b/review2/h09b5b-review2-probes.tar.gz` (380 entries, sha256 `0b898e65…`); `INDEX.txt` lists each probe, output and overlay. Line numbers below are in snap-d.

## Findings, most severe first

**R2-1 (high): a site that ValueInstances makes, failing inside code only AllInstances makes, now sends the whole build to ValueInstances.**
- Repro: `p/r2/demand_site_in_by_name_trial.pts`. `Bad.make<T>` is paired speculatively with the by-name `m.make`. Inside it, a rest-parameter value call `g(maybe(), x)` fails at its own site.
- Base and Node print `by name 5`. Snap-d throws `TypeError: Expected Box<number>, got Box<any>`; debug shows "not within later spec=1 … falls back".
- Cause: `reportIn` makes the trial foreign for any site report (`lower.go:438`, also `:1419`), so the report stands. The site's level is ValueInstances, but ValueInstances never lowers `Bad.make<number>`. `Within` therefore fails and the build falls back. Base just took that trial back.
- Fix: only make the trial foreign when every site's level is at most the trial pairing's level; otherwise take the trial back. `proto/` (`atSitesWithin`) does this.

**R2-2 (high): the same failure through `statementSites`.**
- Repro: `p/r2/statement_site_in_by_name_trial.pts`. A rest-parameter call that lowers fine, then later in the same statement a `Dog[]` kept as an `Animal[]` (D-094). That is the trial's own failure, which base takes back silently.
- Base and Node print `by name 1`; snap-d throws a TypeError.
- Cause: `here()` puts `statementSites` ahead of the trial's own code (`narrowing.go:113`), so the D-094 report becomes the site's and stands.
- Fix: check the trial's own code before `statementSites`. `proto2/` does this and also fixes R2-1 and R2-4.

**R2-4 (high): leaving out a site removes it from every instance of the function it is in, including the program's own.**
- Repro: `p/r2/site_key_across_instances.pts`. `f<T>` has a statement with the by-name call `m.make(1)` followed by a D-094 failure that only `f<Dog[]>` hits, and only a speculative trial makes `f<Dog[]>`.
- Base and Node print `own instance 1 / la 2 / use 1`. Snap-d throws a TypeError inside the program's own `f<number>`.
- Cause: the report stands (R2-2), the by-name site (AllInstances only, so `Within` doesn't apply) is left out, and sites are keyed by call node alone (`leavesOutSite`, `narrowing.go:328`).
- Fix: key sites per instance, or never let a trial's report stand for a site node that the program's own code also lowered. `proto2` happens to avoid this case.

**WT (high): the working tree's removal of `lowerPending()` (`instances.go:932`, `:939`, `:971`) matters.**
- Repro: `p/r2/leftover_dangling_instance.pts`.
  1. `w<Dog[]>` doesn't fit, so the erased copy of `Holder<Dog[]>.make` is left pending.
  2. The next trial, `la2<Dog[]>`, lowers that leftover. `helper<Dog[]>`, which the leftover calls, is credited to that trial (`generics.go:1202` credits every instance made while a trial runs).
  3. The trial fails on its own and is taken back, which drops `helper<Dog[]>`. The kept erased copy then emits `pts_value_from_number((pts_array *)…)` with the call missing, and clang rejects it (`out/ld.base.c`, `out/cc-saved-0.c`).
- Base: its AllInstances C doesn't compile, so the build falls back and use throws a TypeError. Base's emit-c printed that uncompilable C, which makes this a natural C176 emit-c trigger.
- Snap-d prints Node's output. The working tree has both AllInstances' and ValueInstances' C rejected, builds DeclaredInstances, and throws a TypeError.
- Fix: keep the `lowerPending()` calls. The underlying bug is that instances made by code a trial doesn't own are added to `t.instances`/`t.funcs`.
- `making++`: three probes (`total_trial_own_instance.pts`, `total_own_demand_context{,2}.pts`) give identical blames with and without it. In each case the `d.subst` chain reaches the same node-level pairing, or the program's own code had already hit the Box total.

**R2-3 (medium): `blameOf` is exponential on strongly connected use graphs, and past `maxBlameSteps` the report counts as the program's own.**
- Repro: `p/r2/dense_use_cycle_outside_N.pts`, N generic functions that all call each other, lowered outside any trial and reached only through a speculative `Holder<Dog[]>`.
- Steps: 1,958 for N=6, 13,701 for N=7, over 100,000 for N≥8 (`narrowing.go:159`). From N=8 the program falls back as a whole, so victims throw; Node prints the victims' values.
- Cause: results that were cut aren't cached (`:209`), so nodes are re-explored.
- Fix: compute the merge by plain reachability with a visited set (merge is a union), and handle `outer` separately.

**Gap (no regression): levels aren't capped by the code that contains the demand.**
- Repro: `p/r2/value_pairing_inside_by_name_trial.pts`. A value pairing whose demand exists only inside a by-name trial's instance is blamed at ValueInstances' level. `Within` fails, and the build falls back like base (both TypeError; Node prints `by name 5`).
- Related, by reading only: `valueLevel` puts object-literal methods defined with method syntax on a function-typed property (`asFunction`) at ValueInstances, though only AllInstances makes them. That is conservative.

**Docs.** D-199 says "no call has fewer instances than the base's fallback gave it". R2-1, R2-2 and R2-4 contradict it until fixed.

## Prototypes (`proto/`, `proto2/`)
`proto2` (`proto2-vs-snap-d.patch`) fixes R2-1, R2-2 and R2-4. Under it:
- the 22 `run/generic_fallback_*` tests match their `// expect:` blocks;
- the 7 `errors/generic_fallback_*` guards keep their errors;
- the driver tests pass.

It does not fix R2-3 or the gap.

## Checked and fine
- **emit-c:** identical between base and snap-d on 1,459 of 1,480 programs from `pts/tests`, docs and examples; the 21 that differ are all the slice's tests. The 74 D-198 probes match except `total_cliff_interface`. Lowering leftovers outside the trial changed no generated C.
- **Round-1 probes:** all reproduce as you described, and the working tree gives the same results on them. `fanout2_7`'s 0.6 → 2.0 s is the clang check on 67k lines.
- **siteDemandAt for dispatched calls** (`p/r2/dispatched_rest_site.pts`): only the failing site loses its demand; the interface call, the value call and the method's other call keep theirs (matches Node; base loses two of them).
- **Within and laterLowering:** the memo is consistent, and each level is lowered at most 8 times. When the later level's C is rejected the result is the same as base; I worked that out but didn't run it.
- **relaxedCastUse:** its choice is sound.
- **Trial-owned dense cycles** take one step and are fine (`p/r2/dense_use_cycle_N.pts`).

## Interrupted round 3 (snapshot `snap-f`, `9132cb9d1`): recovered evidence, no saved verdict

Parent recovery note, 2026-10-21: [h09b5b-review3-probes.tar.gz](h09b5b-review3-probes.tar.gz) was present in `/tmp/h09b5b/review3` at resumption (295 entries). No final prose report was present; this note is **not** a reconstructed reviewer report. Its `INDEX.txt` records probe replays, emit-c comparisons, and an overlay that compares `blameOf` against a synthetic use-graph oracle. With `outer` links, it records 66 insufficient cuts and one empty result for the checkpoint implementation; without `outer`, no mismatch. These are unresolved test-design/review evidence until independently rerun and their graph assumptions checked. The resumed named reviewer is investigating them before the slice is ticked. The original archive's model attribution is claude-opus-5-5; resumed agents inherit gpt-6.1-sol.
