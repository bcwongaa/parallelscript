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
