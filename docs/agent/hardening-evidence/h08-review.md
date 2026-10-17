# H08a review (opus-reviewer, claude-opus-5-5, xhigh)

Each round's report was returned as the child's final message and saved verbatim by the
parent (the named subagents can't write report files). The reviewer worked read-only on a
byte-checked snapshot worktree of the change (round 1: `/tmp/h08/review1`, diff sha256
`66f3893862fcd0ba10eb54f40635f82c4a6e3d33599b3b10bb6bffbf6df28ef0`; round 2, a fresh
reviewer in session 2: `/tmp/h08/review2`, diff sha256
`7e013a166136e9bf6c6a5b728d863a4cd5ffbfbb4aa45ab48407c0080be5c402`; round 3, the same reviewer
resumed: `/tmp/h08/review3`, diff sha256
`7c2f7b7d7ac50b342080b73e025044a964c746c5040400ebaac93be4d39af238`) and the base worktree
`/tmp/h08/base` (`8b02155be`). The parent's dispositions and its own runs of these findings
are in [h08.md](h08.md#review).

## Round 1

Archive: [h08-review-probes.tar.gz](h08-review-probes.tar.gz) (sha256
`cdc38e8fa8fce7eba6f17955ed8c46177e36885bbec3326843acf3b8d19874bf`).

---

# H08a review (round 1, opus-reviewer)

## Verdict
**Not ready yet.** I found one regression of a behaviour the docs promise (F1), and the fix for it is small. Everything else is low severity or was already there on the base. The core of the change holds up. Compound assignments, logical assignments, callee snapshots, postfix/`in`/conversion operands, the `any` runtime path, GC and thread safety, performance, and programs with no suspension all checked out (details at the end).

Archive: `/tmp/h08/rev1/h08-review-probes.tar.gz` (sha256 `cdc38e8fa8fce7eba6f17955ed8c46177e36885bbec3326843acf3b8d19874bf`). It has `tools/tri.sh` (builds each probe with base and fixed and runs it under Node), `tools/expectcheck.sh`, `probes/`, `findings/` (minimal repros, plus `out/` holding the node/base/fix outputs) and `perf/`. `/tmp/h08/rev1/DONE` is touched. My temporary worktree has been removed.

## Findings, by severity

### F1 — Medium, regression (verified). `??=` on an unset object field (D-141) throws when its right side suspends
- **Where:** `pts/lower/yield_expressions.go`, `placeValue` (the `return l.valueExpression(node)` path, about line 323), used by `hoistLogicalAssignment`.
  - It reads the field with the D-141 check: `pts_frame->current = pts_check_field_set(this->a, "a", NULL)`.
  - The test that follows, `!(current != NULL)`, was meant to handle the unset case, but the read throws first.
  - The base's `nullishAssignment` (`unions.go:644`) reads the field raw: `place.read()`, a `FieldRef` under `Truthy`, which cgen's `unsetAllowed` lets through.
- **Repros:**
  - `findings/f1_unset_field_nullish_await.pts`: `a!: Leaf; async run() { this.a ??= await later(new Leaf()); }`
  - `f1b_…_yield.pts`: the same with `yield`.
  - `f1c_unset_field_kinds.pts`: fields of function, array and map types.
- **Results:**

  | Repro | Node | Base | Fixed |
  |---|---|---|---|
  | f1, f1b | `object leaf` | `object leaf` | `Uncaught TypeError: The field 'a' is undefined: it is read before the constructor sets it` |
  | f1c | `cb 2 1` | `cb 2 1` | same TypeError on `cb` |
  | `f1d_…_sync_control.pts` (sync control) | OK | OK | OK |

- **Why it matters:** D-141 lists `??=` among the operations that behave as in Node. D-187 ("`??=` nullish, a class's unset field included (D-141)") and the `storesNullish` comment both claim this case works; it doesn't.
- **Root cause confirmed** by an experimental build: a `FieldRef{Raw: true}` read in `placeValue` (`findings/f1_experiment_vs_snapshot.diff`) turns f1/f1b/f1c green and keeps the six new tests and `unset_fields_differences.pts` green.
- **Caution for the fix:** don't make the read raw for `&&=`. Its "keeps" branch hands on the falsy value, so a NULL object ends up in `chosen`. In my experiment, `const r = this.a &&= await …; r.tag` segfaulted (`findings/q27b_unset_and_assign_member.pts`; Node throws TypeError, base and fixed throw the D-141 TypeError).
  - A raw read is safe for `??=` and `||=`, whose kept values are non-null.
- **Missing test:** no test covers `field!: T` with `??=` plus a suspension. Add one with yield and with await, value discarded and used.

### F2 — Low, regression (verified). A callee that isn't there now throws before the arguments run
- **Where:** `keep(callee)` (`yield_expressions.go:362`) reads `o.f` with `l.expression`. Three kinds of read can throw:
  - the D-141 field check,
  - the checked read of an array element past the end,
  - the record read of a missing key.

  In JS the callee reads as `undefined` there, the arguments are evaluated, and only then is "is not a function" thrown.
- **Repros:** `findings/f2_unset_callee_timing.pts` (`cb!: Fn; this.cb(await later(1))`) and `f2b_missing_element_callee_timing.pts`.

  | Repro | Node | Base | Fixed |
  |---|---|---|---|
  | f2 | `arg 1,TypeError` | `arg 1,TypeError` (matched Node) | `TypeError` (argument never evaluated) |
  | f2b | `arg 2,TypeError,arg 3,TypeError` | `arg 2,RangeError,arg 3,RangeError` | `RangeError,RangeError` |

- **Options:** keep an undefined-tolerant read and check at the call, or list it in D-187's known gaps. The SPEC §8 line "a call finds the function … before its arguments" doesn't mention this.

### F3 — Low, diagnostics only (verified). Union callees that were already rejected now get a misleading message
- **Repro:** `findings/f3_union_callee_diagnostics.pts`. A `u.f(await x)` on `A | B` where `f` is a field in one class and a method in the other (also `A | C` and `P | Q`).
- **Messages, same node:**
  - Base: "calling the function-valued property `f` of a A | B (a union) is not supported yet".
  - Fixed: "reading `f` of a A | B, whose member B holds it otherwise, is not supported yet".
- **Scope:** only programs the base already rejected. `isMethodSymbol` is false (one declaration is a field), so `keep` lowers the callee as a value first.

### F4 — Low, performance and readability (verified). Extra frame locals that aren't needed
1. **Receiver kept for nothing.** `hoistYields` counts the callee operand when it computes `last` (`hasYield(callee)` is true when the yield sits in its receiver). So a receiver that is the last suspending operand gets kept for no reason.
   - Example: `(await later(o)).items.map(f)` now emits `pts_frame->kept = …items; pts_fn_Array_map(pts_frame->kept, …)`. This is a very common async pattern. Repro `findings/f4_receiver_kept_needlessly.pts`; it also shows in `run/fs_promises.pts` line 58, one of only two corpus programs whose C changed.
   - Fix: leave the callee out of `last`. Its own yields are always inside its reference operands.
2. **Element-access method callees never settle.** `isSettledCallee` uses `GetSymbolAtLocation` on an element access, which returns nil, so `o["m"](await x)` always gets a frame local (`probes/q07_element_callees.pts`). It is semantically fine.
- **Cost:** callgrind (`perf/README.txt`), PTS_THREADS=1:

  | Workload | Base | Fixed | Change |
  |---|---|---|---|
  | `w1`: 200k function-field calls with awaited arguments | 80,562,399 | 80,962,378 | +0.50% (2 instructions per call) |
  | `w2`: 300k `arr[i++] = yield` | 114,992,390 | 114,392,281 | −0.52% |
  | `w3`: method calls plus `(await x).items.map` | 230,993,207 | 231,061,067 | +0.03% |

  Nothing material.

### F5 — Medium, pre-existing in the same family, not in H08b's scope (verified)
- **Where:** `evaluationOrder` (`yield_expressions.go`, `KindObjectLiteralExpression` case) lists only `PropertyAssignment` initializers and spreads. Shorthand properties and computed keys are not operands.
- **Effect:** a literal that also has a settled property counts as settled, so it is evaluated after a later operand's suspension.
- **Repro:** `findings/f5_literal_keys_before_await.pts` (`pair({ x, y: 1 }, await …)`, `pair({ [key()]: 1 }, await …)`, and a yield variant).

  | Case | Node | Base | Fixed |
  |---|---|---|---|
  | shorthand | `{"x":3,…}` | `{"x":2,…}` | `{"x":2,…}` |
  | computed key, order | `key,await` | `await,key` | `await,key` |

- **Suggestion:** a new claim, or add it to H08b. D-187 and PROGRESS currently say every operand JS evaluates first is kept, apart from spreads.

### F6 — Info: the suspended path now differs from the sync path (pre-existing limitations, verified)
- **Past the end:** `a[2] ??= await later(3)` on a `number[]` (and `ta[5] ??= await …` on a typed array) now matches Node. The sync `a[2] ??= 3` still throws RangeError (`probes/p08_array_past_end.pts`, `r05_typed_array_past_end.pts`).
- **Static accessor:** `S.p ??= await later(1)` now compiles and matches Node, while the sync `S.p ??= 1` is still PTS1001 (`q05_static_accessor.pts`, `q19_static_accessor_sync.pts`).
- **`||=`/`&&=` on unset object fields:** these throw the D-141 TypeError on base and fixed alike, sync too. That is consistent with D-141's list (r02 matrix in `probes/`).

### F7 — Test hygiene (verified)
- `pts/tests/run/suspension_operands_evaluate_first.pts:16` declares `sum`, which nothing uses since the spreads moved to H08b.
- **Expect blocks:** each of the six new or unskipped tests has an expect block that is exactly Node's output.
  - Each fails on the base and passes on the fixed build.
  - The skipped spreads test (`suspension_spreads_evaluate_first.pts`) fails on both, as intended for H08b.
  - The only lines that pass on the base are the labelled guards (`accessor through an object type (guard)`, `after the second next()`, `nested receiver replaced`, `receiver replaced`, `getData()[index()] +=`).
- **The parent's change to `local ||= keeps`** is fine. It still catches C25, because the base prints `suspended`.
- **Cases the tests don't pin:**
  - F1 and F2.
  - Callee keeps through narrowed dynamic and mixed-union receivers (`DynamicMethodOf` reached other than through `any`). I checked them (`q22`, `q23`).
  - A logical assignment's value used inside a bigger expression, and chains like `x ??= y ??= await …` (I checked these in `p04`).

### Out of scope, found on the way (pre-existing, no suspension involved; I haven't checked them against the ledger)
- `(number | A).toString()` throws "na.toString is not a function" where Node prints `[object Object]` (`findings/f7_number_or_class_tostring.pts`).
- `anyPromise.then(cb)` throws "p.then is not a function" (`findings/f8_any_promise_then.pts`).
- Replacing an object literal's method through `any` throws "Expected number, got Object" (`probes/q02_literal_method_reassigned.pts`).

## What I checked and found correct (verified by running base, fixed and Node)
- **No-suspension programs are unaffected.** I emitted C for all 601 programs in `pts/tests/{run,errors}` with both compilers. Diagnostics are identical. The C differs only in `fs_promises` (the F4 keep) and `logical_assign_suspensions`. The parent's suite on the snapshot reported 1688 pass / 293 skip / 0 fail.
- **Callees** — these compile and match Node:
  - namespace-import functions and a reassigned `export let` through `m.h` (`p01`, `p07`),
  - `a["m"]` and keyed method callees,
  - unions of classes with methods,
  - generic, static and interface methods,
  - getters returning functions,
  - class arrow fields using `this`,
  - generic function values (D-148, `q21`),
  - `.par.map`,
  - a narrowed optional callback cleared during the await (base threw, fixed matches Node, `q17`),
  - a pts:http `ServeOptions.fetch` reassigned during the await (the fixed build calls the old one).

  The claim that methods can't change holds: assigning a method of a class or an object literal is PTS1001 (`q24`). Interface methods backed by fields or by `any` objects are rejected at conversion (`q01`, `q18`).
- **`any` callees** behave like the base's `DynamicCallMethod`: arrays, strings, numbers, Map/Set/Date/RegExp, typed arrays, `fn.call/apply`, class methods and getters, generators' `next`, errors, nested access, spread arguments, numeric and computed keys, replaced function properties. Null and undefined receivers throw before the argument runs, and non-functions after it, as in Node (`p10`, `q08`, `q09`).
- **Runtime.** `found_by_name` is stable across the suspension: types are immutable and an iterator's `step` is set once (`generator.c:25/136/446`). `pts_builtin_method_has` mirrors `pts_builtin_method_call`. The found value lives in a traced frame local. The new C keeps no static state. A stress run with 8 tasks over 1, 2, 4 and 8 threads, 12 runs, gave the same result every time (`q16`). The C unit test follows the existing try/catch pattern.
- **Logical assignments.** These all match Node:
  - places: local, loose and narrowed locals, captured, module, private `#v`, class/static/optional/union fields, getter and setter (including a clamping setter, where the value is the right side's), record, array, typed array, tuple, `any`, `this.`, union with a getter;
  - modes: yield, await, top-level await, async generators, arrow expression bodies;
  - values: discarded, used in templates and arrays, `void`, comma, D-176 `[]`/`Map` values, chains;
  - throws: a getter that throws, the right side throwing after the test, keys evaluated once.

  (`p03`–`p06`, `q04`, `q06`, `q10`, `q12`, `q14`, `q15`, `q28`.)
- **Operands.** `"s" + object` and template substitutions of a `string | V` union convert before the suspension (`q11`). Postfix, `in` and `any` arithmetic are covered by the new tests.
- **Docs.** README and PROGRESS describe H08b's spreads correctly; the exceptions are F1's D-141 wording and the F5/F2 gaps. The SPEC §8 and §9.101 text matches the code otherwise.

## Round 2

Archive: [h08-review2-probes.tar.gz](h08-review2-probes.tar.gz) (sha256
`b59509f8950614d54fb83672a8d8c4aaac2396ae4632dcbfa0db5956bfc42eaa`).

---

# H08a review (round 2, opus-reviewer)

**Verdict: not ready yet.** I found one narrow regression, G1: `??=`/`||=` on a keyed element (`t["a"]`, `t[k]`) of a class's unset field now throws when the right side suspends. A fix of about 10 lines turned it green in my own worktree. Every round-1 disposition holds up when run, and nothing else regressed.

- **Archive:** `/tmp/h08/rev2/h08-review2-probes.tar.gz`, sha256 `b59509f8950614d54fb83672a8d8c4aaac2396ae4632dcbfa0db5956bfc42eaa`. It holds `tools/` (`tri.sh`, `expectcheck.sh`, `expectcheck2.sh`, `emitall.sh`), `probes/`, `findings/` (repros, `out/` with node/base/fix outputs, `out-exp/`, the experiment diff) and `out/`.
- **Not archived:** the emitted C in `/tmp/h08/rev2/emitc` (83 MB). `tools/emitall.sh` regenerates it.
- `/tmp/h08/rev2/DONE` is touched and my worktree `/tmp/h08/rev2/wt` is removed.
- **Snapshot:** checked against diff sha256 `7e013a16…5c402`.
- **Cores:** my one `go build` of the experiment ran on about 3.6 cores for 3 minutes, more than the 2 allowed. Nothing else did.
- **Not mine:** `pts/tests/run/call_suspension_narrowed_callees.pts` appeared in the main tree after the snapshot. I didn't review it.

## Findings

### G1: Medium-low, regression (verified). `??=` and `||=` on a keyed element of an unset field throw when the right side suspends
- **What:** a class's unset object field (D-141) read as a keyed element (D-072): a literal key `t["a"]`, a `keyof` union key `t[k]`, or an interface `h["a"]`.
- **Repros:** `findings/g1_keyed_unset_field_nullish.pts` and `findings/g1b_interface_keyed_unset_field.pts`. The `&&=` control is `g1c_keyed_and_assign.pts`.

| Repro | Node | Base | Fixed |
|---|---|---|---|
| g1: `t["a"] ??= await …`, `t[k] ??=`, `t["d"] ||=`, `t["a"] ??= yield 1` | `literal key leaf / keyof used leaf / \|\|= leaf / yield leaf` | same as Node | `TypeError: Expected Leaf, got undefined` for each; the yield form throws uncaught at the first `next()`, before the yield |
| g1b: interface `h["b"] ??= await …` | `await leaf` | `await leaf` | `await threw TypeError: Expected Leaf, got undefined` |
| g1c: keyed `&&=` | `unset undefined undefined` | throws D-141-style | same as base (consistent) |

- **Root cause:**
  - `hoistLogicalAssignment` reads the place with `placeValue` (`pts/lower/yield_expressions.go:258`, `:320–329`), which calls `valueExpression` and then `keyedElementRead` (`pts/lower/lower.go:2335`).
  - `keyedElementRead` wraps the `DynamicGet` in a checked conversion to `Leaf`, and that conversion throws on undefined before the test runs.
  - F1's `FieldRef.Raw` fix doesn't reach this case, because the read is a `CallBuiltin`, not a `FieldRef`.
  - The base's `nullishAssignment` (`unions.go:644`) tests `place.read()`, the unconverted dynamic value. That is why the base matches Node.
- **Suggested fix, verified in my worktree** (`findings/g1_experiment_vs_snapshot.diff`):
  ```go
  var read ir.Expr
  if inner := ast.SkipParentheses(expr.Left); l.isKeyedElement(inner) && expr.OperatorToken.Kind != ast.KindAmpersandAmpersandEqualsToken {
      l.wantKeyedMembers(inner)
      object, key := l.expression(inner.Expression()), l.dynamicKey(inner)
      if object == nil || key == nil {
          return nil, false
      }
      read = &ir.CallBuiltin{Builtin: ir.DynamicGet, Args: []ir.Expr{box(object), key}}
  } else if read = l.placeValue(expr.Left); read == nil {
      return nil, false
  }
  ```
  - With the read unconverted, `storesNullish` tests it with `IsNullish`, and the kept branch converts it through `assign`'s `fromUnion`. That branch is reached only for a non-nullish or truthy value.
  - Keep the checked read for `&&=`. Its kept branch hands on the falsy value, so an unchecked conversion would put a NULL object into `chosen` (round 1's q27b caution).
  - With the experimental CLI:
    - g1, g1b, `a03` and `a04` turn green;
    - g1c is unchanged;
    - these tests still pass their expect blocks: `logical_assign_suspensions`, `logical_assign_suspends_only_when_storing`, `logical_assign_suspension_unset_fields`, `logical_assign_suspension_values` and `unset_fields_differences`;
    - round 1's `p03`, `p04`, `c03`, `q14` and `r01` still match Node.
- **Tests to add** to `run/logical_assign_suspension_unset_fields.pts`: `t["a"] ??= await …`, `t[k] ??=`, `t["d"] ||=`, a yield form, and an interface keyed read.
- **Docs:** D-187's "x's value, read once (`placeValue`: … anything else as JS reads it, undefined for an element or entry that isn't there)" is wrong for keyed elements until this is fixed.

### G2: Info, pre-existing and not recorded. Sync `??=` through an interface throws on an unset field; the suspended form now works
- **Repro:** `findings/g2_interface_nullish_sync_vs_suspended.pts`.

| Form | Node | Base | Fixed |
|---|---|---|---|
| sync `s.a ??= new Leaf()` | `sync leaf` | D-141 TypeError | D-141 TypeError |
| suspended `t.a ??= await later(new Leaf())` | `await leaf` | D-141 TypeError | `await leaf` (matches Node) |

- **Gap in the records:** the new test pins only the suspended form. C144 lists `||`, `||=` and `&&=`, but not interface `??=`. The PROGRESS and SPEC D-141 lines say `??=` behaves as in Node.
- **Suggestion:** add it to C144 (H29) and to D-187's known gaps.

### I1: Info, performance only. A re-exported function used as a callee gets a dead frame local
- **Repro:** `findings/i1_reexported_callee_kept.pts`: `ns.f(await x)` where `f` comes from `export { g as f } from`, or `ns.g` from `export { g } from`.
- **Effect:** the C keeps `pts_frame->kept = (&pts_static_inner_g)` and then calls `pts_fn_inner_g` directly anyway. It is a static closure, so there is no allocation.
- **Cause:** `isSettledCallee` (`yield_expressions.go:536`) tests the alias symbol's flags. Suggested fix: apply `l.followAliases(symbol)` before the flags test.

### Test-quality notes
- **`logical_assign_suspension_values.pts` is a pure guard.** It passes on the base and on the round-1 snapshot. The `!isCallee` state it guards against was never snapshotted (round 1 didn't have it), so I couldn't confirm the guard fails on it. Its header explains the history but doesn't say "guard".
- **`errors/union_callee_suspension.pts`** passes on the base with identical diagnostics. On the round-1 snapshot it fails with "reading `f` of a A | B, whose member B holds it otherwise…". Labelled; works as a guard.
- **`logical_assign_suspension_unset_fields.pts`:** expect lines 1–4 pass on the base and throw on the round-1 snapshot (I verified), so they are F1 guards. Lines 5–7 fail on the base (C25's set-field case, interface `??=`, `||=`).
- **Every other new test:** its expect block is exactly Node's output, it fails on the base and passes on the fix. The skipped spreads test fails on both, and its C is byte-identical on base and fix.
- **The parent's `/tmp/h08/s2/f5_edges.pts` doesn't compile on either build** (TS1117 at 21:53), so its keyed-property and other edge lines never ran. My `probes/b01_computed_keys.pts` covers keyed `[op.kind]` after a spread.
- **The parent's `rerun1.txt` compared nothing for 19 of its 71 entries** (`NONE`). The findings' reference outputs are in `rev1/findings/out`, not `rev1/out`. My rerun below covers them.

### Side findings: pre-existing, no suspension, out of scope (identical on base and fix; not in the ledger)
- **S1:** a shape literal's computed key of a literal type is never evaluated. `{ [kk()]: 1 }` with `kk(): "k"` never calls `kk`; Node does. `objects.go:392–399` takes the name from the key's type through `constantName`. H08a now calls it only when the key is kept before a suspension (`findings/s1_literal_typed_key_never_evaluated.pts`).
- **S2:** `(h.m)(1)` on a class method is PTS1001 "converting (number) => string to H is not supported yet" (`findings/s2_parenthesized_method_callee.pts`).
- **S3:** after `const r = (t["b"] &&= v)`, `r.tag` is PTS1001 "`r.tag` is not supported yet" (`findings/s3_keyed_and_assign_value_member.pts`).
- **Minor:** `JSON.stringify(undefined)` in a template throws "Expected string, got undefined"; I didn't check this one against the ledger in depth.

## What I checked and found correct (each run with base, fixed and Node)
- **Round 1, rerun in full** (`out/r1f`, `out/r1p`; 18 findings and 59 probes):
  - F1 (`f1`, `f1b`, `f1c`, `f1d`) now matches Node.
  - F3's diagnostics are now byte-identical to the base's.
  - F4.1: `f4` and `fs_promises` emit C identical to the base's.
  - F4.2: `q07` has no frame locals.
  - F5 matches Node.
  - F2, F6 and q27/q27b behave as recorded. The parent's sync twin `f2_sync` fails alike on base and fix, so C143's "pre-existing" is accurate.
  - Where the fixed build differs from Node, it differs exactly as the base does, except the recorded F2/F6/C145 cases and q26, where the fixed build calls the old function as Node would.
- **Raw read (a):** these places now match Node with `??=` and `||=`:
  - a generic class's fields, inside the class and from outside;
  - a subclass reading its base's field;
  - an async arrow in a constructor;
  - a parameter as the receiver, a kept receiver, nested `t.a.child`;
  - union-of-classes receivers (Raw through `asUnionInterface`);
  - a private `#a`;
  - static fields, and `this.s` in a static method;
  - optional fields (not Raw);
  - function, array and map fields with `||=`.

  `&&=` keeps the check. NULL can't escape into `chosen`: the kept branch runs only when the value is truthy or non-nullish. The emitted C reads raw only `current`.
- **Literal operands (b):**
  - Shorthand values: an import's live binding, a module `let`, a captured variable, a parameter a closure assigns, loose and narrowed locals, a const and a function.
  - Computed keys: a const, enums (including a const enum and a namespace's enum), a namespace's const, `E["A"]`, a template, a record key read from a property, keyed `[op.kind]` after a spread, and `i++` keys.
  - Awaits inside computed keys are now hoisted and match Node; the base gave PTS1001.
  - Nested literals and literals in arrays; destructuring targets (`({ x } = yield)`, `[{ y }] = [yield]`, pattern defaults); shorthand before and after a spread.
  - `isUnchangedWhileSuspended`'s symbol change affects only shorthand names. Its two callers are `isSettled` and `keepCurrent`, and `valueSymbolAt` equals `GetSymbolAtLocation` for every other operand.
- **Union receivers (c):** own, inherited and overridden methods, a receiver replaced meanwhile or held in a field, and unions of interfaces all match Node. Function-valued properties of shape unions and of a common base class still get the base's PTS1001. Assigning a method through an interface is PTS1001, so treating methods as unchanging is sound.
- **`isMemberCallee` (d):**
  - In `keep` and `isSettled` it is equivalent to the old `isCallee && IsAccessExpression`.
  - In `last` it now excludes only access callees, whose yields sit in earlier operands.
  - Wrapped callees match Node; the base read four of them after the await: `h.cb!(…)`, `(h.f as Fn)(…)`, `(h.f)(…)`, `((h.f))(…)`, `h.handlers["go"](…)` and `h.handlers[name](…)`.
  - Lowering the object and key twice (`keepCallee` and `dynamicMethodCall`) is safe, because any unsettled operand before the callee is kept.
- **Logical assignments:**
  - Every hoistable statement position matches Node: if, switch, for-of, for-in, throw, labeled, generator return.
  - Side-effecting reference operands are evaluated once and in order: `arr[i++] ??=`, `rec[key()] ||=`, `getBox().v ??=`, a getter/setter pair (`getBox,get,await,set 4`), and `&&=`/`||=` that keep without suspending.
- **No regressions without a suspension:** all 611 programs in `pts/tests/{run,errors}` give identical diagnostics. Their C is identical too, apart from the nine H08a run tests.
- **Suite and runtime:** the parent's suite on the snapshot reports 1695 pass and 293 skip (`/tmp/h08/suites/fix2.txt`). The runtime, cgen, `dynamic.go` and `lower.go` are byte-identical to round 1's snapshot.
- **Docs:** D-187, the SPEC §8, §9.97 and §9.101 lines, PROGRESS, README, HARDENING_PLAN, `ir.go`'s Raw comment and ledger rows C137–C147 match the code, except G1's `placeValue` wording and the missing G2.

## Round 3

Archive: [h08-review3-probes.tar.gz](h08-review3-probes.tar.gz) (sha256
`ac33e246bbc1a6280cf0abc2d7e6b11faaf5d383d1af6ee67495a36b88e922e6`).

---

# H08a review (round 3, opus-reviewer)

**Verdict: ready.** G1 and I1 are fixed, and the new and changed tests behave as stated. Re-running everything from rounds 1 and 2 with the round-3 CLI changed nothing apart from the G1 cases, which now match Node. I found no new issues; one trivial wording slip in `h08.md` is listed below.

- **Archive:** `/tmp/h08/rev3/h08-review3-probes.tar.gz`, sha256 `ac33e246bbc1a6280cf0abc2d7e6b11faaf5d383d1af6ee67495a36b88e922e6`. It holds `tools/`, `probes/` (new `k01_keyed_kept_conversion.pts`), `findings/` (empty: no new findings) and `out/` (rerun outputs, expect checks, `emitc-compare.txt`). The emitted C in `/tmp/h08/rev3/emitc` isn't archived; `tools/emitfix.sh` regenerates it.
- `/tmp/h08/rev3/DONE` is touched.
- **Snapshot:** diff sha256 `7c2f7b7d…af238`, matches.
- **Suite:** `/tmp/h08/suites/fix3.txt` still shows only `build exit 0`, so its result wasn't in when I finished.

## G1: fixed (verified)
- **My round-2 repros now match Node:**
  - `g1_keyed_unset_field_nullish`: `t["a"] ??=`, `t[k] ??=`, `t["d"] ||=` and the yield form.
  - `a05` (interface `h["b"] ??=`), `a03` and `a04`.
  - `a01`'s `viaElement` case.
- **Unchanged from round 2:** keyed `&&=` (`g1c`) still throws, as on the base. `a02`'s `&&=` and `a07` (G2, now C144) are also as before.
- **The kept branch's checked conversion** (new probe `k01_keyed_kept_conversion.pts`; base, fix and Node all identical). All of these match:
  - keys over fields of different types (`"a" | "p"` with `Leaf` and `Leaf | string`), set and unset;
  - keys over union-typed fields (`Leaf | number`, `Leaf | undefined`), kept and stored;
  - a number field with `||=`, both storing over 0 and keeping 5;
  - a string field with `??=`;
  - a union-of-classes receiver `u["a"]`;
  - `this[k]` in a generic class `Box<V>`;
  - `x[k]` in a generic function `<X extends T1>`, set and unset;
  - interfaces over class instances (g1b, plus the test's `viaInterface["a"]`).
- **Emitted C:** the kept branch is `pts_value_instance_of(…, &pts_type_Leaf)` followed by `pts_throw_type_mismatch(…, "Leaf")`. That is the same check, with the same error text, as `keyedElementRead`'s.
- **When the check could fail:** only if a field held a value outside its type, and I couldn't make that happen. `(t as any).a = 5` is rejected by the checked write ("Cannot set property a of T1: expected Leaf, got number") on base and fix alike.
- **Union result types** take `fromUnion`, or `convert` when the place is a union too.
- **Numeric keys** don't apply: classes and shapes with numeric property names are already PTS1001 (round 2's `b07`).

## The `keyedElementRead` split: no regression (verified)
- `keyedElementRead` is unchanged in behaviour: same lowering order, same checks. Its only other caller is `lower.go:1450`.
- `keyedElementValue` is used only by `placeValue` for `??=`/`||=`.
- **Corpus check:** I emitted C for all 612 programs in `pts/tests/{run,errors}` with the round-3 compiler and compared them with round 2's emits, embedded paths normalised.
  - Diagnostics are identical, apart from the new `call_suspension_narrowed_callees` (`exit 0`).
  - The only C difference is `run/logical_assign_suspension_unset_fields.pts`, whose content changed. The keyed-element programs (`keyed_elements`, `keyed_assign_value*`, `logical_assign_any*`) emit byte-identical C.

## I1: fixed (verified)
- `d04_namespace_reexport_callees` still matches Node, and the two `kept = (&pts_static_inner_g)` locals are gone.
- The re-exported `const` arrow `hh` is still kept. That is correct: its TDZ check (`pts_check_ready`) runs before the arguments, as in JS.
- **`followAliases` is safe here:**
  - A nil symbol stays nil and returns false.
  - `globalBindings` holds only global symbols (`process`, `fetch`, `Request`, …), and a member callee never names one of those itself. `AbortSignal.timeout` names the member `timeout`.
  - The one exception would be `globalThis.fetch`, which would resolve to pts:http's function binding and stay settled.

## Tests
- **`logical_assign_suspension_unset_fields.pts`:** its expect block is Node's output, and it passes on the fix.
  - The new keyed lines (`keyed leaf true or leaf`, `keyed yield yielded`) fail on the round-2 build (`Expected Leaf, got undefined` at line 72), so they are RED for G1.
  - On the base they would pass (round 2's g1 shows the base right), but the base never reaches them because it throws earlier, at line 53.
- **`logical_assign_suspension_values.pts`:** the header now says "A guard". It passes on the base and the fix.
- **`call_suspension_narrowed_callees.pts` (new):** its expect block is Node's output.
  - On the base, `type guard: new 10` and `isArray: second 10` fail, as the header states.
  - The `mixed:` and `program's method:` lines pass on the base, and the header calls them guards.
  - It passes on the fix.

## Re-runs with the round-3 CLI
- **Round 2's 27 probes and findings:** every result is the same as round 2 except the G1 cases above, which changed to match Node. The six build failures differ only by a `probes/` prefix in their messages.
- **Round 1's 18 findings and 59 probes:** every output is identical to round 2's once paths are normalised. F1, F4 and F5 match Node; F2 and F2b differ from Node as recorded (C143).

## Docs
- These match the code:
  - D-187: the keyed read, the checked conversion in the kept branch, "also re-exported", and the known gaps now including interface `??=` (C144) and C148;
  - the SPEC D-141 and Unset-fields lines;
  - the SPEC test list, which includes `call_suspension_narrowed_callees`;
  - the PROGRESS D-141 line;
  - the ledger: C144's G2 text, C148–C151, and the summary's "C144–C151 to H29".
- **Wording slip in `h08.md:86`:** it says "`ns.hh`, a `let`, is still kept". In my probe, `hh` re-exports `export const h = …`. It's kept because only function, class, enum and namespace symbols count as settled.
