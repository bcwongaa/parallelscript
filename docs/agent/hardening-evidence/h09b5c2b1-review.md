# H09b5c2b1 — independent C251 caps review

Date: 2026-10-22. Reviewer `a035ba7eeb694c00c`, inherited current `gpt-6.1-sol` runtime/xhigh configuration, read-only on the new containing-code-cap slice. This is **not** the provider-refused H09b5b review or acceptance. No retry, rewording, model/provider switch or replacement of that stopped assignment occurred.

## Scoped verdict

**Ready; no verified in-scope findings or outstanding production suspicion.** Reviewed `pts/lower/containing_code.go`, `narrowing.go`, the lowerer integration, graph/model/bounds tests, public source/driver regressions, runtime fixture and smoke entry. Production hashes stayed unchanged through review. Compiler-local availability state is rebuilt for each lowering; runtime code is untouched.

The reviewer checked raw-key/effective-level separation, completed descriptor users, demand/value and class dependencies, least-fixed-point cycles, conservative statement provenance and budget exhaustion. The review does not establish a global correctness proof, accept H09b5b, or cover deferred method/wrapper flow.

## Reviewer execution

- `GOMAXPROCS=1 go -C /home/bryan/projects/parallelscript test -count=1 -p 1 -parallel 1 -run '^TestContainingCode' -v ./pts/lower ./pts/driver`: exit 0, **8 pass events** on the initial test snapshot.
- Scratch-only completed-registry observation overlay: exit 0, **14 source cases / 15 pass events**. No raw pairing/site capped below a fallback in which that same raw key remains live. Covers shared own/value roots, generic classes, nested values, repeated demands, constant-key/spread sites and rolled-back controls.
- Plain snapshot CLI versus fresh base CLI and strict-ESM Node: all 14 sources build with both compilers; **12 current Node matches versus 6 base, six gains, zero regressions**. The two rolled-back C250-style controls remain identical pre-existing nonmatches; they are not new findings or fixed-scope claims.
- Fixture: base exit 1, `Expected Box<Dog[]>, got Box<any>`; current and Node exit 0, `by name 1 0`.
- Scratch harness's initial Python alias and snapshot-basename collisions were setup failures, corrected without parent-source edits, not implementation defects. Their tool results remain in the reviewer transcript; they are not both standalone raw logs in the probe archive. The archive retains corrected scripts and a separate invalid-TypeScript-probe log.

Safe archive: `h09b5c2b1-review-probes.tar.gz`, **49 regular files**, no executables, generated binaries, symlinks or hardlinks. SHA-256 `0d194816547ce7df1d7119c5afd69a72653709010ede300071ee10332f462640`. Parent verified that digest, safely extracted into a new empty directory and inspected the source differences and observation hook before independent replay. Scripts ran from parent scratch outside the extraction with Python `-I`, not from the extracted archive.

## Final test follow-up

After the reproducer's final plain/generic-class tables landed, the **same reviewer** resumed read-only on `pts/lower/containing_code_caps_test.go:16–94` and `pts/driver/containing_code_caps_test.go:13–52`. No new findings; scoped ready verdict unchanged. The generic class substitutions, isolated subtests, cap checks and required by-name-descriptor assertions remain meaningful. Nonfatal `assert.Check` still records a genuine RED while permitting later clean-lowering assertions to execute. No additional heavy commands were run for this follow-up; the earlier eight-event result is not misattributed to the final tables.

Coverage nuance: textual module-level own-root placement before/after is placement coverage, not evidence that descriptor discovery was late. `TestContainingLevelsUseEveryLateDescriptorHit/declared` directly exercises the late DeclaredInstances hit; its ValueInstances sibling and source value-root controls cover shared availability. An optional extra source-level late-declaration scenario was suggested, not a defect or an acceptance requirement.

## Parent verification

Parent independently ran the finalized public tables with exact `9e60deebf` production sources via a scratch Go overlay: **9 fail / 9 pass events**, six real failing source/driver children plus their three failed containers, and all eight shared-root children plus their container pass; **no build failures**. Final current lower/driver packages: **261 pass / 0 fail**, empty stderr, all 6,669 source hashes and CLI unchanged.

Parent native replay of all 14 reviewer programs plus the reproducer's generic-class RED: all 15 sources build with base/current, **13 current Node matches versus 6 base, seven gains, zero regressions**. The same two C250 controls remain nonmatches. Replay receipts and completed full-suite results are recorded in the main [slice evidence](h09b5c2b1.md): final build and exact required four-job suite exit 0, **2,301 pass / 335 skip / 0 fail**, corpus **263 pass / 37 skip**, all ten tested packages PASS, empty stderr, all 6,669 source hashes and CLI unchanged. The earlier wrapper-limited incomplete run is retained, not counted green. Final acceptance remains the parent's responsibility after commit/push.
