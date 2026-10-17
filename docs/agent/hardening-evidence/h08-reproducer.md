# H08 reproducer report (opus-reproducer, claude-opus-5-5, xhigh)

Returned as the child's final message on 2026-10-18 and saved verbatim by the parent (the named
subagents can't write report files). The child worked in the isolated worktree
`.claude/worktrees/agent-a212cb879a857f709` at `8b02155be`, scratch `/tmp/h08/repro/`, against the
parent's base worktree `/tmp/h08/base`. Its archive is
[h08-reproducer-probes.tar.gz](h08-reproducer-probes.tar.gz) (sha256
`621c359dacbe556e0a4551b23feb3649e328ec63fab85f2a0176b5da3c5be1c3`); its proposed tests are under
`deliverables/tests/` there. The parent's own runs of these claims are in [h08.md](h08.md).

---

# H08 reproducer report: suspension reference snapshots

## Summary

All five reported defects reproduce on the base (8b02155be). The family turned out wider than the reports said. There are five independent root causes, all in `pts/lower/yield_expressions.go` plus one line in `lower.go`, and each shows up the same way with `yield`, `await`, top-level await and async generators:

- **F1 (C08): compound assignment reads the target after the resume.** This happens when the target is a getter (class, inherited, `this.`, inferred object-literal accessor), any `any` place, or a field read through a **union of classes** (the union case is new).
- **F2 (C25): logical assignment suspensions are hoisted unconditionally.** `??=`, `||=`, `&&=` suspend even when they don't store. They also test the place after the resume, so a change made meanwhile decides whether to store. This holds for every place kind tested.
- **F3 (GPT #2): the callee is looked up after the arguments' suspension.** This affects every function-valued property, element, `this.m` field, `any` method, nested chain, record or array entry, and getters that return the function (the getter runs after the resume).
- **F4 (n1, wider): postfix `i++` / `i--` counts as "settled".** It evaluates after the suspension (also in a comma, under `void`, or as a plain `=` index). In compound assignments it evaluates twice and the wrong element is written.
- **F5 (new): other operands D-145 wrongly treats as settled.** Spreads (call, `new`, array literal, object literal; also a kept `...call()`), `in`, and binary operators with an `any` operand (valueOf) run after the suspension.

What I ran: the claim probes, 14 hand probes, a generated matrix of 2310 cases × 2 runtimes, and 5 new RED tests plus the unskipped C25 test through the real harness. I did not fix the compiler. Everything is in my worktree and the archive.

## Per-claim reproduction (ran)

Command: `PTS_HOME=/tmp/h08/base /tmp/h08/base/pts-bin/pts build X.pts -o X.bin && ./X.bin` against `node --no-warnings --experimental-transform-types X.mts`, through `/tmp/h08/repro/tools/cmp.sh`. Every run exited 0 on both sides.

| Claim | PTS | Node | Verdict |
|---|---|---|---|
| n1 `arr[i++] -= yield 1` / `+= await` | `gen i 2 100,95,300` / `async j 2 100,101,300` | `gen i 1 95,200,300` / `async j 1 101,200,300` | confirmed |
| r2_acc_base (class accessor) | `resume1,get,set,…` | `get,resume1,set,…` | confirmed (its interface half is correct) |
| GPT #2 `06_residual.pts` | `accessor 12 / any 12 / method new 10` | `11 / 11 / old 10` | confirmed |
| Grok #2 `06_yield_order.pts` | `reads after first 0` | `reads after first 1` | confirmed |
| C25 `logical_assign_suspensions.pts` (unskipped) | `x 1 5 { a: 1 } { b: 1 } 5` / `[ 1 ]` | `… 0` / `[]` | confirmed |

The ledger had C08 as "unverified (reports' probes not yet rerun)". Both report probes are now rerun and confirmed.

## Findings beyond the claims (ran; minimal repros in `explore/`)

- **F1, union receiver (new).** `const o: U1|U2 = pick(); o.p += yield 0`, with `o.p = 2` set while suspended: PTS stores 12, Node 11. All 42 matrix cases for this target are WRONG.
- **F1, other getter shapes.** Inherited and `this.` accessors, inferred object-literal accessors, and `any` holding an object with a getter all behave like the class accessor: getter after the resume, value read after it. Top-level await and async generators behave the same way (`e10`, `e13`).
- **F1 guard: an object typed as an interface whose value is an accessor is correct** (the D-178 path). So the literal accessor fails only when tsc infers the literal's own type.
- **F2, wider than the C25 test.** Captured, module, field, static, accessor, interface, shape, `any`, array, tuple, typed array, record, index-signature, union and `this.` places are all wrong, with yield and await.
  - On a "keeps" place the suspension still happens.
  - On a "stores" place the test is made after the resume: `box.n ??= yield` with `n = 2` set meanwhile leaves 2; Node stores 10.
  - Getters are called after the suspension, and the setter is skipped or called wrongly (`accessor &&= stores: get,suspended,set 10` versus PTS `suspended,get`).
  - A key that suspends plus a value that suspends both yield: `rec[yield "key"] ??= Number(yield "value")`.
- **F3, receiver handling.** Receivers that need keeping (a swapped variable or field, a nested receiver replaced) are already right. Only the function value is read late.
- **F4.** `pair(i++, yield i)`: PTS yields 0, Node yields 1. `[k++, yield k, k]` yields 5 instead of 6. A closure reading `i` while suspended sees the old value. `arr[i++] = yield` writes the right element but bumps `i` after the suspension. `typed[i++] +=` and `arr[i--] *=` write the wrong element.
- **F5.** `sum(...xs, yield)` gives 113 (Node 13), and `sum(...getXs(), yield)` too, because only the array reference is kept. An iterable spread runs its iterator after the resume. `{...o, z: yield}` runs o's getter after it. `"b" in rec` gives true (Node false). `pair(v + 1, await …)` with an `any` valueOf calls valueOf after the resume (6/10, Node 2/10). `v < 3` and `v * 2` behave the same; unary `-v` is correct.

**Rejected without any suspension (not H08 defects):**
- Static accessor compound assignment (`assigning to a property that has no setter`).
- `super.p` (`super keyword is not supported yet`).
- Tagged templates.
- Class-valued variables with `new`.
- Assigning an interface method signature.
- Converting `{greet: arrow}` to an interface with a method signature.

**Out of scope, found on the way (no suspension involved):**
- `"s" + anyWithValueOf` calls toString and gives `sv`, where Node calls valueOf and gives `s1`. This contradicts SPEC §8 on D-142. Probe: `explore/side/any_concat_valueof.pts`.
- `(iface as any).m = fn; iface.m(2)` throws `TypeError: Expected number, got Object`. Probe: `explore/side/iface_method_any_swap.pts`.
- Writes through `any` that add a property, or put `undefined` into a literal's number field, throw. This is documented under D-L10; I worked around it in the tests.

## Matrix (ran)

`matrix/gen.py` (run with `python3 -I`, exhaustive, no randomness) writes 186 batched programs and 2310 cases. Groups: compound (7 operators including string `+=`, 23 targets, 3 right-side forms), logical (3 operators × stores/keeps), call (19 callee kinds), reference (16 index/receiver forms × 4 operators), operand (24 kinds), and nested. Each case comes in yield and await variants and has a control twin with the suspension replaced by 10. `node_check.py` confirms all programs run under Node. `run.py` builds and runs everything (2 jobs, timeouts on every run), splits programs that fail, and builds controls for every case that isn't OK.

Final run: **2310 cases: OK 864, WRONG 1368, DIAG 78, CRASH 0, ICE 0.**
- Every WRONG case's control is OK, so every WRONG is suspension-specific.
- All 78 DIAG are `super` property access, and their controls are DIAG too.
- Run 1's 16 CRASH cases were D-L10 artifacts of my generator (their controls crashed too). I fixed the generator and kept run 1 in `matrix/run1/`.

| Family | WRONG targets |
|---|---|
| compound | accessor, inherited, `this.`, literal accessor, `any`, `any` element, `any` holding an accessor, union field: 42/42 each |
| logical | every target; on locals only the "keeps" cases (18) |
| call | 13 of 19 callee kinds |
| reference | `[i++]`, `[i--]`, `any[i++]`, `typed[i++]` with every operator; `get…().p` for `+=` and logical operators |
| operand | postfix forms, spreads, `new` with spread, `in`, `any` binary operators |
| nested | accessor/any targets as an argument, in a conditional, with two suspensions; logical as an argument |

The guards pass: fields, shapes, arrays, tuples, typed arrays, records, interface accessors, kept calls, `++i`, keys and receivers swapped while suspended. Full list: `matrix/out/summary.txt` and `results.json`.

## Proposed RED tests

They are in my worktree under `pts/tests/run/` and copied to `deliverables/tests/`. Every `// expect:` block is Node's exact stdout, written by `tools/expect.py`. Control twins with no suspension match Node on the base (`deliverables/controls/`); the one control difference is a deliberate real `await` that remains.

1. `compound_assign_suspension_reads_target_first.pts`: F1 with yield and await. Covers class, inherited, `this.` and literal accessors, an interface-typed guard, `any` property and element, `any` holding a getter, and the Grok getter count. On the base: `class accessor: suspended,get,set 12 => 12` against expected `get,suspended,set 11 => 11`.
2. `logical_assign_suspends_only_when_storing.pts`: F2. Covers stores/keeps for field, accessor (getter count, setter), `any`, record with `key()` evaluated once, array `[i++]`, local, with yield and await. On the base: `field ??= keeps: suspended => 2` against `: => 1`.
3. `call_suspension_reads_callee_first.pts`: F3. Covers literal, class field, `this.m`, interface, `any`, record, array, getter callee, receiver guards, with yield and await. On the base: `new 10` against `old 10`.
4. `suspension_reference_operands_evaluate_once.pts`: F4 in assignment targets (n1 exactly, `i--` nested, `=`, typed, `any`, `??=`, await). On the base: `i 2, 100,95,300` against `i 1, 95,200,300`.
5. `suspension_operands_evaluate_first.pts`: F4 and F5 as call operands (postfix forms, spreads, iterable, `new`, object spread, `in`, `any` valueOf), with yield and await.
6. `logical_assign_suspensions.pts` with the skip line removed (diff in `deliverables/tests/logical_assign_suspensions.unskip.diff`). Its comment "PTS awaits each `later`…" will need past tense once fixed.

Harness run (`GOMAXPROCS=2 PTS_REQUIRE_NODE=1 go test -p 2 -count=1 -timeout 40m -run '^(TestRun|TestRunMatchesNode)$/^(…six names…)$' ./pts/tests/`): exit 1, all 12 subtests FAIL with stdout diffs. Output is in `deliverables/red_harness_base.txt`.

## Where each family goes wrong (file:line on 8b02155be)

- **F1.** `keepCurrent` keeps nothing when the receiver `isDynamic` or the target `isAccessor` (`yield_expressions.go:393`). `isDynamic` also counts unions (`exceptions.go:399-401`), which explains the union case. Even if it did keep, `readKeptAndStore`'s getter branch calls the getter again and ignores `kept` (`lower.go:3093`; compare `:3087`). That is exactly the "getter twice" reason D-145 gave for the gap.
- **F2.**
  - `hoistYields` sends only `&&`, `||`, `??` to `hoistShortCircuit` (`yield_expressions.go:104`).
  - Logical assignments fall into the generic loop (`:107-141`). There `evaluationOrder` (`:446-449`) appends the right side and its yields are hoisted unconditionally (`:122`).
  - `readsTargetFirst` excludes them (`:375`), so the test happens later inside `compoundAssignment`: `lower.go:2910` (records), `:2912` (`nullishAssignment`, `unions.go:644`), and `:2921` (`||=`/`&&=`).
  - `nullishAssignment` drops the right side for non-nullable types (`unions.go:656`) even though its suspension has already run.
- **F3.** For a call, `evaluationOrder` puts only `referenceOperands(callee)` before the arguments, meaning the receiver and key (`yield_expressions.go:452-460`), never the callee's value. `valueCall` (`closures.go:438`), `dynamicCall`/`dynamicMethodCall` (`dynamic.go:130-160`) and the interface/shape call paths look the property up while the call is lowered, after the resume.
- **F4.** `isSettled` has no `PostfixUnaryExpression` case; prefix `++`/`--` is handled at `:308-309`. Postfix falls through to the generic tail (`:325-334`), where its operand, a name, is settled. `keepCurrent`'s `l.expression(target)` (`:399`) then runs `i++` once, and the assignment's `assignmentTarget` runs it again. Comma and `void` inherit the problem through `:316` and `:300-302`.
- **F5.**
  - `SpreadElement` is grouped with parentheses as settled (`:301`). The `SpreadAssignment` of an object literal is handled the same way through the literal branch (`:476-484`, `:334`).
  - `keep` keeps only a spread's operand, not its elements (`:254-271`).
  - Binary operators are settled when both operands are (`:316`), with no `isPrimitive` check like the unary rule at `:313`, and no exception for `in`.

## Constraints and suggestions for the fix (suggestions are labelled)

Constraints:
- Each getter must run exactly once, before the suspension. The setter must not be followed by a second read; for `&&=` and `||=`, JS does GetValue once and then PutValue.
- Reference operands (object and key) are evaluated once, before both the test and the right side.
- Keep D-145's settled classification for names and constants (benchmarks), but it must not hold for anything that runs code or reads mutable state.
- Kept values live in frame locals (D-109).
- `assignedValue` typing (D-176) and record and `any` place semantics (D-L10 checked writes) must be preserved.
- For class methods (ir.Plain, which can't be reassigned in PTS) only the receiver needs keeping. Don't add a function-value keep there, for performance.

Suggestions:
- **F1.** Let `keepCurrent` keep accessor, dynamic and union targets, and make the getter branch of `readKeptAndStore` use `kept` (converted) the way the field and dynamic branches already do.
- **F2.** Add a `hoistLogicalAssignment` modelled on `hoistShortCircuit`. Hoist and keep the reference operands, keep the current value once, test it, and put the right side's hoisted yields plus the store, which uses the kept read and never reads again, inside the "stores" branch. Its result goes into a `chosen` local.
- **F3.** For property and element callees that can change (function-typed members, getters, `any`), keep the receiver and the function value before the arguments, and call the kept value with the kept receiver as `this`. Object-literal method shorthand uses `this`. For `any` this may need a "call with this" dynamic builtin.
- **F4 and F5.**
  - Return false from `isSettled` for postfix `++`/`--`, spreads, `in`, and binary operators with non-primitive operands.
  - Make `keep` snapshot a spread's elements, for example by copying into a new array, and do the same for object spreads.
- Update the D-145 "Known gaps" line and SPEC §8 (the `.pts` paragraph at SPEC:620) once the fix lands.

## Files

My worktree, `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a212cb879a857f709`, at HEAD 8b02155be with nothing committed:
- the five new tests and the unskipped C25 test in `pts/tests/run/`;
- the five C submodules, initialised.

Scratch directory `/tmp/h08/repro/`:
- `claims/`, `explore/`, `tools/`;
- `matrix/` (`gen.py`, `run.py`, `node_check.py`, `out/`, `run1/`);
- `deliverables/` (`tests/`, `controls/`, `base_runs/`, `red_harness_base.txt`, `worktree_status.txt`).

Archive: `/tmp/h08/repro/h08-reproducer-probes.tar.gz` (1.5 MB, no binaries); `/tmp/h08/repro/DONE` is touched.

I did not touch `/tmp/h08/base`; I only used its CLI.
