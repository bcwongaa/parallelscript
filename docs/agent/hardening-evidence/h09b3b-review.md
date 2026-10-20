# H09b3b review reports

The `reviewer` subagent's reports, saved by the parent from its final messages (claude-opus-5-5, xhigh; the Agent call passed the `opus` alias). The reviewer was read-only on the parent's tree; each round reviewed a byte-checked snapshot worktree of the change. Probe archives: [h09b3b-review-probes.tar.gz](h09b3b-review-probes.tar.gz) (round 1). What the parent did with each finding is in [h09b3b.md](h09b3b.md).

## Round 1: snapshot `/tmp/h09b3b/snap2r` (a9cbf8ac3 plus the uncommitted change)

**Verdict: not ready.** F2, F3 and F4 are regressions I verified. In each one, the slice's new code fails while lowering with `AllInstances`. The driver then silently falls back, which strips the instances from every demand by name in the program, so calls that ran correctly on the base now throw TypeError. F1 also breaks programs that compiled and ran correctly on the base. Once F2–F4 are fixed and F1 is decided, the remaining items are gaps or numbers to record in D-194.

All probes are in `/tmp/h09b3b/review1/h09b3b-review-probes.tar.gz` (sha256 `20d3eeaf7b075cf0c50e3d365bd68b20ae6dbc117fb10418608eb68d4029a223`; `INDEX.txt` inside). Each `probes/rNN_*.pts` has its `.node`, `.base` and `.snap` output beside it. `tools/tri.sh` re-runs a probe on all three.

The fallback hides the failure, so I built a small command, `tools/allinst-{base,snap2r}`, that lowers with `AllInstances` alone (`driver.EmitCWith`) and prints the real error. Its results for every probe are in `probes/ALLINSTANCES.{base,snap}.txt`. While probing I briefly copied a test harness into the snapshot as `pts/zz_review_emitwith`; it is removed, and `git status` shows the same 32 entries as before.

### Findings

**F1 (medium, regression): `call`/`bind`/`apply` of a class's generic method with an `undefined` or `null` thisArg is now PTS1001.**
- Where: `pts/lower/methods.go:475` sends generic methods to `methodCallOrBind`, and `:496` converts thisArg to the class.
- Repro: `r01_call_undefined_this.pts`, e.g. `m.id.call(undefined, 5)` or `m.make.bind(undefined)(6)`.
- Node and base both print `5 / 6 / 7 / 8`. The snapshot exits 1 with `PTS1001: converting undefined to Maker`.
- This is a hard compile error, not hidden by the fallback. It matches the existing non-generic rule (`r01b`: PTS1001 on both trees).
- Related cases where the base was silently wrong now give an honest PTS1001: another object as thisArg (`r02`, base printed `a:1`), another instantiation of a generic class (`r26`, base `s:5`), an interface-typed thisArg (`r28`, base `a:1`). That is an improvement, but still a compile break.
- Fix: keep the base's path for a `null`/`undefined` thisArg, or accept it and record it in D-194 as following D-044's class-method rule.

**F2 (high, regression): an adapter whose instance result doesn't convert statically to the context's result sends the whole program to the fallback.**
- Where: `pts/lower/forwarding.go:268`, `l.convert(call, to.Result, node)`; `:243`, `adaptedArguments`, has the same exposure.
- Repros: `r12` (`const t: (x: number) => Box<number | string> = local`), `r25` (`(x: any) => Box<number>`, where tsc infers T = any) and `r32` (a parameter typed `(x: number) => Box<number | undefined>`).
- The `AllInstances` lowering reports `PTS1001: using a Box as a Box` and the driver falls back.
- Effect: an unrelated `m.make(4)` through an interface prints `5` on the base and in Node, and `TypeError Expected Box<number>, got Box<any>` on the snapshot.
- Fix: build the converted result inside `quietly`. On failure, either return `nil, false` (the old adapter) or box the instance's result and convert it dynamically, which gives at worst the base's run-time TypeError. Add these contexts to `generic_values_nongeneric_contexts` next to an interface call, so `TestCompilerFailsCleanly` pins them.

**F3 (medium, regression): a keyed call whose key is a dynamic value that tsc types as literals also falls back.**
- Where: `pts/lower/element_calls.go:250`, `notSupportedYet` for a key that isn't `ir.String`.
- Repro: `r13`, `m[yield 1](5)` in a `Generator<number, void, "make" | "wrap">`.
- Error: `PTS1001: calling a method of a {…} by a key of type value`, then the fallback, and the interface call regresses from `5` to TypeError.
- A dynamic receiver would hit the `!isObject` half of the same check; I reasoned that but did not verify it.
- Fix: don't report there. Either convert the key to string with a check, or lower the old DynamicGet call from the values already lowered.

**F4 (high, regression): forwarders and adapters of generic functions with a rest parameter crash C emission.**
- Where: `forwarding.go:126`, `:146` and `:253` pass the instance's rest parameter, typed as `ir.Rest`, to `erasedArguments`. `instances.go:353` then boxes it.
- Error: `PTS9001: internal compiler error while emitting C: cgen: cannot box ...number[]` in `AllInstances`, then the fallback.
- Repros: `r18` (`rest.bind(undefined)`) and `r18b` (a plain method value `new ClassMaker().all`). The interface call goes from `5` on the base and in Node to TypeError on the snapshot.
- In `r17`, one rest method value in the file makes the optional and default lines throw too, though they work without it (`r17c`).
- `r20`: an adapter of a rest generic also crashes `AllInstances` first, then reports the base's PTS1001.
- Fix: pass the rest local as its array to `erasedArguments`, or have `takesAs` (`forwarding.go:96`) reject rest parameters. Add a test with a rest method value and a bound rest function.

**F5 (medium, growth): pairing every wrapper with every demand of its erased type.**
- Repros: `growth/gen.py`, `gen_sep.py`, `builds.txt`.

| Program | Base | Snapshot |
|---|---|---|
| `s_60` (60 method values, each passed to its own one-type consumer) | 10,461 lines of C, build 2.1 s | 93,741 lines, build 72.5 s, 607 MB |
| `g_40x40` | 9,691 lines, 2.4 s | 64,691 lines, 51.8 s |

- `s_60` makes 3,600 forwarding instances and 3,600 class instances, of which 60 of each can run.
- This is the known C172 class (the declaration control `sd_60` makes 43,942 lines and takes 43.7 s on both trees). Method values never paired before, and each pairing now costs a forwarder plus an instance.
- The parent's corpus measurement grew only about 0.3%.
- Fix: record these numbers in D-194's known gaps (H09b5). A cheap cut is to have a non-dispatched class method's forwarder call the class's instance directly, instead of a by-name lookup plus an inner demand.

**F6 (medium, gap): a shorthand property for an interface's generic method still throws.**
- Where: `closures.go:620` handles `KindPropertyAssignment` only.
- Repro: `r22`, `const m: Maker = { make }` gives `TypeError Expected Box<number>, got Box<any>` on both trees, where Node prints 6. The parenthesized, function-expression, method-value, bound and declaration forms pass.
- Fix: use `adaptedExpression(node)` for both property kinds.

**F7 (medium, gap): a generic value for a non-generic interface method keeps the erased copy.**
- Repro: `r30`, `interface NumberMaker { make(x: number): Box<number> }` with `{ make: local }`, or an inline generic arrow, throws TypeError on both trees.
- Cause: `functionAsMethod`'s `adaptSkipping` never instantiates. This is squarely "an adapter that hides the instances" from the task.
- Fix: an instantiating path with `skip` = 1.

**F8 (low–medium, gap): a keyed call with a yield or an await in its arguments throws on both trees.**
- Where: `element_calls.go:204`, `l.kept[callee] != nil`.
- Repros: `r09` (`im[key](yield 1)`, `c[key](await …)`) and the keyed line in `r08`.
- This is where C196 meets C205 and await. Handle it the way `constantKeyCall` handles kept receivers, or record it as a known gap.

**F9 (low, pre-existing): a method value's `length` doesn't count an optional parameter.**
- Where: `methods.go:227`.
- Repro: `r19`. Node prints `2 2 2`; base and snapshot print `1 1 1`.

**F10 (low, test quality):** no new test pins F1, F2, F4, F6, F7 or F8. `TestCompilerFailsCleanly` passes only because the tests avoid these forms.
- The changed line in `generic_call_forms_any_boundaries` (an honest key replaced by a lying key that names no method) is justified, because the honest form now matches Node and is covered by `generic_element_access_runtime_keys`.

**Design note (not a defect), for D-194:**
- A method value's typed call follows D-148's value-call rule: an `any` argument is converted with a check and finds the instance. The dotted by-name call keeps the erased copy (D-191). The guard test pins both, so D-194 should state it.
- Keyed lookups are uncached: 3M calls took 153–343 ms keyed against 78–125 ms dotted (`perf/`, a noisy host). Low.

### What I checked that held
- **New tests:** the 10 new and 2 changed tests match Node, or their expect blocks for the two no-diff ones. All are RED on the base, and all lower under `AllInstances` alone.
- **Reproducer's 575-case matrix:** 540 OK / 28 TYPEERR / 7 DIAG. None moved from OK to not OK; 352 moved TYPEERR → OK and 46 ICE → OK. The 28 TypeErrors are the liar guards and `.call`/`.apply` typed `Box<unknown>` with `as`; the 7 DIAGs are static `C[key]`.
- **Corpus and matrices:** I lowered 1,463 programs (`pts/tests`, examples, the matrix cases) with `AllInstances` alone on both trees. None that lowers on the base fails on the snapshot, and 56 more lower.
- **Fixed behaviour:**
  - wrong `this` in `call`/`bind`/`apply` (`generic_method_call_bind_this`, `r16b`);
  - overridden methods read through the base type now run the override, where the base silently ran the base class's method (`r05b`);
  - dispatched values no longer crash with PTS9001;
  - class-method destructuring, including inherited, overridden, nested and for-of (`r06b`).
- **Keyed calls:** `o[key](args)` evaluates object, key, then arguments, as Node does (`r03`; the base read the method first). Subclass overrides, `this[key]`, `keyof` keys, `K extends keyof` keys, keys built at run time and arrow properties all work (`r07`, `r31`).
- **Other forms that work:**
  - forwarders of forwarders (`r29`);
  - generator and async results (`r24`);
  - type-parameter receivers (`r31`);
  - parallel tasks (`r10b`);
  - library callbacks such as `reduce`, `flatMap` and `Array.from` (`r11`);
  - `name`, `length` and `console.log` of adapters and bound functions (`r27`, `r33`).
- **Runtime and memory safety (code reading):**
  - `pts_method_instance_find_keyed` compares flat strings by `byte_len` and `memcmp`;
  - an entry without a function ends the search, as in the dotted lookup;
  - accessors are skipped, and fields that aren't objects or functions return NULL, which takes the `Otherwise` path;
  - the field load is the same relaxed atomic as the dotted path;
  - forwarding instances read the wrapper's closure struct (`Shares`; checked in the emitted C);
  - trials drop forwarders created in dropped code;
  - forwarders are deduplicated by name, signature and key, so pairing doesn't loop.
- **Boundaries and build:** D-155's lies still throw; `go vet` and `gofmt` are clean; the new lower unit test passes.

Key files: `/tmp/h09b3b/snap2r/pts/lower/forwarding.go`, `element_calls.go`, `methods.go`, `closures.go`, `instances.go`.

## Round 2: snapshot `/tmp/h09b3b/snap3r`

**Verdict: not ready yet, but close.** All seven round-1 findings the parent reworked (F1–F4, F6–F8) are fixed. The take-back that fixed F2 has two holes, R2-4 and R2-5, and each is a regression against the base (a calculation that used to work now throws TypeError). Both are confined fixes in `forwarding.go` and `quietly`. After them, and a rule for R2-1 and R2-2 (code or D-194), I'd call it ready, with F5, R2-3 and the listed gaps recorded.

Probes are in `/tmp/h09b3b/review2/h09b3b-review2-probes.tar.gz` (sha256 `48a868a41b126ca99b804d3aa599e20f8a02e41eafb77a6b3a50058875a4841a`; `INDEX.txt` inside; copied as [h09b3b-review2-probes.tar.gz](h09b3b-review2-probes.tar.gz)). `/tmp/h09b3b/review2/DONE` is touched. Each probe has its `.node`, `.base` and `.snap` output beside it. `tools/allinst-snap3r` is the round-1 checker rebuilt against snap3r: it lowers with `AllInstances` alone and prints the error the fallback hides. Nothing was added to the snapshot tree; `git status` still shows the same 39 entries.

### Round-1 findings re-checked (`probes/round1/`)

| Finding | Status | Evidence |
|---|---|---|
| F1: `call`/`bind` with an `undefined`/`null` thisArg | Fixed | `r01` matches Node (`5 6 7 8`); `r02`/`r26`/`r28` (methods that use `this`, thisArg of another type) stay PTS1001, see below |
| F2: adapter result that doesn't convert sends the build to the fallback | Fixed | `r12`, `r25`, `r32`: the interface call prints 5 again and `AllInstances` lowers. The adapter lines keep the base's TypeError, and `r12`'s `Box<unknown>` line now matches Node. Not fixed when the adapter also needs a nested adapter (R2-4) |
| F3: keyed call with a dynamic key | Fixed | `r13`: the interface call prints 5 |
| F4: rest-parameter forwarders crash | Fixed | `r17`, `r17b`, `r18`, `r18b` match Node. `r20` (rest adapter) now compiles and matches Node, where the base gave PTS1001 |
| F6: shorthand `{ make }` | Fixed | `r22` |
| F7: generic value for a non-generic method | Fixed | `r30` |
| F8: keyed call with yield/await arguments | Fixed | `r08` keyed line and `r09` match Node |
| F5: code growth | Unchanged, as stated | `s_60` and `g_40x40` emit the same 93,741 and 64,691 lines. Fine to record under C172/H09b5 with the parent's idle-host numbers |
| F9: `length` with an optional parameter | Pre-existing, recorded | `r19` |

Under `AllInstances` alone, the only round-1 probes that still fail are the expected ones: pre-existing PTS1001s, and the deliberate PTS1001 for `this`-using methods (`r02`, `r26`, `r28`). On those three, PTS1001 is preferable to the base's silent wrong `this`. Please record that choice in D-194.

### New findings

**R2-4 (medium, regression): a taken-back adapter leaves its nested closures behind, and C emission crashes.**
- Where: `pts/lower/forwarding.go:288–293` takes back only `fn`. A nested adapter created by `adaptedArguments` (`:252`), whose enclosing function is `fn`, stays in `l.lowered`.
- Repro: `probes/new/q08b_takeback_nested_adapter_only.pts`, with a context `(f: (x: number) => number, y: number) => Box<number | string>` and a value `<T>(f: (x: T, i: number) => T, y: T) => Box<T>`.
- `AllInstances` gives `PTS9001: cgen: run.arrow.adapter.adapter doesn't capture adapted`, then the fallback, and the unrelated interface call regresses from 5 (base and Node) to TypeError.
- The same happens inside a generic function's instance (`q08c`) and through `functionAsMethod` (`q11`, `run.adapter.adapter`).
- `q09` is the same shape without the take-back: it works.
- Fix: take back the way `tryInstance` does. Drop every function whose enclosing-function chain reaches `fn` (`t.owns`), and any demands or erased values they added. Or check that the result converts before building the body.

**R2-5 (medium, regression): a taken-back adapter inside a trial makes the trial fail.**
- Where: the take-back (`forwarding.go:288`) and `quietly` (`:179`) restore `l.errors` and `l.unexplained` but not `l.reports`, which `report` increments (`lower.go:414`). `tryInstance` checks `l.reports == reports` (`instances.go:888`), so the instance is dropped.
- Repro: `q12_takeback_in_trial.pts`, a generic arrow whose body holds `const t: (y: number) => Box<number | string> = local`, called through `<T>(x: T) => Box<T>`.
- Base and Node print `2`; the snapshot prints `TypeError Expected Box<number>, got Box<any>`.
- `AllInstances` lowers fine, so `TestCompilerFailsCleanly` can't see this.
- The same applies to every `quietly` call inside a trial, for example `keyedCall`'s `irType` of other properties. That last case is from reading the code, not reproduced.
- Fix: snapshot and restore `l.reports` too, in the take-back and in `quietly`.

**R2-1 (low–medium, regression; decide or record): `a.m.call(b)` where `b`'s class overrides a generic `m` now runs the override.**
- Repro: `q01_call_subclass_this.pts`. For a method that doesn't use `this`, Node and base print `A.m(1)` / `A.m(2)` and the snapshot prints `B.m(1)` / `B.m(2)`.
- Cause: `mayUseThis` (`methods.go:428`) treats every dispatched method as `this`-using, so the call runs on thisArg under D-044's "the method of thisArg's class runs" rule. That rule already gives non-generic methods the same divergence (`B.p(4)` on both trees).
- `q15` (receiver statically typed as the base class) is wrong on both trees and is not a regression.
- Fix: check every override's body before calling the method `this`-using, so a method that doesn't use `this` runs on its own object. Or record the change in D-194.

**R2-2 (low, regression): `runsOnThisArg` is over-eager.**
- Structural twin: `q02_call_structural_twin.pts`, `a.m.call(twin, 1)` where `Twin` is a structural twin of `A` and `m` doesn't use `this`. `IsTypeAssignableTo` is true (`methods.go:416`), the call goes to `methodCallOrBind`, and the snapshot gives `PTS1001: using a Twin as a A`. Base and Node print `1`.
- Nested object literal: `q14_this_only_in_nested_literal.pts`, a method whose only `this` is inside a nested object literal's method (the literal's own `this`), called with an `undefined` thisArg. `usesThis` descends into object-literal methods. The snapshot gives `PTS1001: converting undefined to A`; base and Node print `lit:1`.
- Fix: run on thisArg only when `mayUseThis` is true. In that check, skip object-literal methods and accessors. `usesThis` is shared from `classes.go`, so give `mayUseThis` its own walker.

**R2-3 (low, pre-existing, the F1 fix is incomplete): `this` used only in a parameter default isn't seen.**
- Repro: `q04_this_in_parameter_default.pts`, `viaDefault<T>(x: T, tag = this.tag)` called with an `any` thisArg.
- Base and snapshot both print `a:1` / `a:2`; Node prints `b:1` / `b:2`.
- Fix: `mayUseThis` should also scan the parameters' initializers.

### New code paths that held
- **Settled keyed callee** (`q05`): across yield and await, the object and key are evaluated before the suspension, and the method is found after it. Reassigning the key and the object variable while suspended doesn't change the call. Output matches Node, with the traces in Node's order.
- **Non-generic keyed calls with a yield argument** (`q06b`): match Node. Library receivers (arrays, Map, string) keyed by name are PTS1001 on both trees (`q06`).
- **Keyed fallback** (`q07`): yield object, yield key, and a key naming no method behave as the base, and `AllInstances` lowers.
- **`mayUseThis` forms** (`q03b`): `this` in an arrow, only in a nested function expression, `super.x()`, a parameter default and a nested literal all match Node. thisArg typed as a subclass or `A | undefined` matches Node; a twin held in `any` gives a checked TypeError (`q02b`).
- **Rest forwarding** (`q10b`): tuple rest and leading-plus-rest work through method values, `bind` and adapters, matching Node. Optional tuple rests and rests of boxes are PTS1001 on both trees (`q10`).
- **Method fields** (`q13`): shorthand of a declaration, bound method values, non-generic methods and keyed calls on such literals match Node.
- **No lowering regressions:** `AllInstances`-only lowering of 1,465 programs (`pts/tests`, examples, the matrix cases) shows nothing that lowers on the base failing on snap3r, and 57 more lower.
- **No output changes:** of 921 programs, 880 emit identical C, 27 fail on both trees, and 14 differ (13 slice tests plus `church_combinators`). The pre-existing ones whose C changed (`church_combinators`, `erased_objects_typed_reads`, `generic_method_erased_objects`) print the same as the base and Node.
- I did not re-run the 575-case matrix, since the parent is running it.

### Test quality
- All 14 new or changed tests match Node or their expect block, and all differ from Node on the base.
- `generic_values_keep_other_instances` fails on snap2r and passes on the base and snap3r, so it is a real guard for F2/F3.
- `generic_method_values_parameters` pins F4.
- Missing pins: R2-4 (a taken-back adapter needing a nested adapter, next to an interface call), R2-5 (a taken-back adapter inside a generic arrow's instance; this needs a Node-compared line, because `AllInstances` passes), and R2-1/R2-2 (a this-free method called on an overriding subclass or a structural twin).

Key files: `/tmp/h09b3b/snap3r/pts/lower/forwarding.go`, `methods.go`, `element_calls.go`, `yield_expressions.go`, `instances.go`.

## Round 3: snapshot `/tmp/h09b3b/snap4r`

**Verdict: not quite ready.** All round-2 findings are fixed except R2-1, which is decided and to be recorded. One new regression remains, R3-1: an instantiating adapter can make a generic class instance the program never makes. When that instance's body doesn't lower, the build silently falls back, and unrelated interface calls throw where the base ran them. A natural program reaches it through `forEach`. A narrow guard plus a D-194 note would make the slice ready; R3-2 only needs recording.

Probes are in `/tmp/h09b3b/review3/h09b3b-review3-probes.tar.gz` (sha256 `c33475293e53b039d7a2ca042926adc579d2ddb223cd9d743ec7824ca718b3d2`; `INDEX.txt` inside; copied as [h09b3b-review3-probes.tar.gz](h09b3b-review3-probes.tar.gz)). `/tmp/h09b3b/review3/DONE` is touched. Each probe has its `.node`, `.base` and `.snap` output beside it. `tools/allinst-snap4r` lowers with `AllInstances` alone, so it shows the errors the fallback hides. The snapshot tree is untouched (still 41 entries in `git status`).

### Earlier findings re-checked (`probes/prior/`)

| Finding | Status | Evidence |
|---|---|---|
| R2-2: structural twin, `this` only in a nested literal | Fixed | `q02`, `q14` match Node. A twin held in `any` passed to a method that uses `this` now gives a checked TypeError (`Expected A, got Twin`); the base was silently wrong (`a:4`) |
| R2-3: `this` in a parameter default | Fixed | `q04` matches Node (`b:1 b:2`) |
| R2-4: taken-back adapter leaves nested adapters | Fixed | `q08`, `q08b`, `q08c`, `q11`: `AllInstances` lowers and the interface call prints 5. The adapter lines keep the base's TypeError, the recorded gap |
| R2-5: take-back inside a trial | Fixed | `q12` prints 2, as do base and Node |
| R2-1: overridden method called on an overriding subclass | Decided, unchanged | `q01`; R3-2 below is its other face |
| Round 1's F1–F4, F6–F8 | Still fixed | All `r*` probes unchanged from round 2 |

Under `AllInstances` alone, every probe now lowers except the expected PTS1001s and tsc errors (`ALLINSTANCES.snap4r.txt`).

### New findings

**R3-1 (medium, regression): an instantiating adapter makes a generic class instance the program never makes, and if that instance doesn't lower the build falls back.**
- Where: `pts/lower/forwarding.go:277`. The calls `signatureType(source, node)` and `signatureType(instantiated, node)` run inside `quietly`, before the tentative mark at `:293`. They create the class instances the instance's types name, and those stand even when the adapter is taken back or its instance's trial fails. Their bodies are lowered later, outside any trial, so a failure there is a hard report.
- Trigger: a generic class whose method narrows a field so that one instantiation can't lower (`s00`: `typeof this.value === "string"` makes `Cell<number>` PTS1001 on both trees, C171's pattern for classes).
- Repros:
  - `s06_foreach_class_instance.pts`: `[1, 2].forEach(keep)` with `keep = <T>(x: T): Cell<T> => …`;
  - `s02_adapter_unknown_class_instance.pts`: `const t: (x: number) => unknown = local`, an adapter that is not taken back;
  - `s01_takeback_class_instance.pts`: `(x: number) => Cell<number | string>`, a taken-back adapter.
- In all three, `AllInstances` reports `PTS1001: converting number to string` for `Cell<number>`, the build falls back, and the unrelated `m.make(4)` interface call goes from 5 (base and Node) to `TypeError Expected Box<number>, got Box<any>`. The base lowers every one of them under `AllInstances`.
- This belongs to D-190's known C176 cliff (class instances can't be taken back), but these adapters reach it in contexts that never name the instance's types: results that are `void`, `unknown`, `any`, wider, or an interface.
- Present since round 1; not found before this round.
- Fix options:
  - don't instantiate where the context's result is `void` (the `forEach` case and `=> void` parameters gain nothing from it) or where the result types differ, which covers all three repros;
  - or check at the checker level that `instantiated`'s result equals the context's before calling `signatureType`.
- In any case, record it in D-194's known gaps with `s06` as the example.

**R3-2 (low, regression inside the decided R2-1 rule): an overridden generic method that never uses `this` no longer compiles with an `undefined`/`null` thisArg.**
- Where: `pts/lower/methods.go:418`. `mayUseThis` is true for every dispatched method.
- Repro: `s07_overridden_call_undefined.pts`. `a.m.call(undefined, 1)` and `a.m.bind(null)(2)` print `A.m(1) / A.m(2)` in Node and on the base; the snapshot gives `PTS1001: converting undefined to A`.
- This follows D-044's class rule, and the SPEC text added in this round says so. Record it in D-194 together with R2-1.
- A cheap way to keep the base's behaviour for a thisArg tsc types as `null`, `undefined` or `void`: dispatch by name on the original receiver. That is exactly what JS does for methods that don't use `this`, and JS throws for those that do.

### What held
- **Take-back** (`tentatively`, `failedSince`, `takeBack` at `forwarding.go:206`): taken-back nested adapters inside a trial, two in one instance body next to an adapter that is kept, match Node and lower under `AllInstances` (`s05`).
- **Take-back by reading the code:** demands and erased values made in nested functions are marked dropped; `l.lowered` keeps everything made since the mark that isn't inside the dropped function; no capture is added to an outer function, since adapters capture only their own `adapted`; instance keys left behind are only names. The class instances in R3-1 are what's left behind.
- **`quietly`** now restores `l.reports`.
- **`usesObject`** (`s03b`, `s04`): `this` in an arrow inside a nested function (it isn't counted, correctly), `super.hello()`, `typeof this.tag` in a type position, and an arrow property of a nested literal all match Node. A `this`-free method with an `undefined` thisArg matches Node.
- **No lowering regressions:** `AllInstances`-only lowering of 1,465 programs (`pts/tests`, examples, the matrix cases) shows nothing that lowers on the base failing on snap4r (57 more lower).
- **No output changes:** of 921 programs, 880 emit identical C, 27 fail on both trees, and 14 differ (13 slice tests plus `church_combinators`, the same set as round 2). The three pre-existing ones with changed C print the same as the base and Node.
- I did not re-run the matrices, since the parent is running them.

### Test quality
- All 14 tests match Node or their expect block, and all differ from Node on the base. All lower under `AllInstances` alone.
- The new lines fail on snap3r and pass on snap4r, so they pin R2-4, R2-5 and the twin, default and literal cases.
- Missing: a guard for R3-1 (a generic arrow returning a class that only some instantiations can lower, given to `forEach` or a `=> unknown` context, next to an interface call), either as a passing line once fixed or as a no-diff pin of the known gap. Also missing: a compile-error test for R3-2 if it's kept as PTS1001.

Key files: `/tmp/h09b3b/snap4r/pts/lower/forwarding.go`, `methods.go`, `callbacks.go`.

## Round 4 (verification): snapshot `/tmp/h09b3b/snap5r`

**Verdict: ready.** R3-1 and R3-2 are fixed, and every earlier fix still holds. The two new findings are not blockers. R4-1 is a new way to reach the cliff D-190 already documents (C176), which the base hits the same way through a plain typed call. R4-2 only gives back some round-3 coverage, and those lines print what the base prints. Record both in D-194.

Probes are in `/tmp/h09b3b/review4/h09b3b-review4-probes.tar.gz` (sha256 `e124c102daef27f93998c876abf84fb3f802fd70d1c806ff82cc5b21d19f51bf`; `INDEX.txt` inside; copied as [h09b3b-review4-probes.tar.gz](h09b3b-review4-probes.tar.gz)). `/tmp/h09b3b/review4/DONE` is touched. The snapshot tree is untouched (42 entries in `git status`, as handed over).

### Earlier findings re-checked (`probes/prior/`, all 73 round 1–3 probes)
- **R3-1 fixed.** The interface calls in `s01`, `s02` and `s06` print 5 again, and `s06` (`forEach`) now matches Node.
- **R3-2 fixed.** `s07` matches Node (`A.m(1) A.m(2)`). `t02b` covers abstract methods with an `undefined`, `null` or `void` thisArg, and a `bind(undefined)` value passed on: each runs the object's own method, where the base crashed with PTS9001. The `.call(…) as Box<number>` lines still throw, by design: tsc types those calls `Box<unknown>`. An `any` holding `undefined` gives a checked TypeError.
- **One expected change:** `r12`'s "unknown result" line is back to the base's TypeError (accepted, recorded under C215).
- **Everything else** prints exactly what it printed in round 3. Under `AllInstances` alone, only the expected PTS1001s and tsc errors remain.

### New findings

**R4-1 (low–medium; a new trigger for the known C176 cliff, not a blocker): an adapter's new demand pairs with unrelated generic values.**
- Where: the demand made at `pts/lower/forwarding.go:286` is paired, like every value demand (D-190), with every erased value of its erased type.
- Repro: `probes/new/t04_adapter_demand_pairs_unrelated.pts`. The demand from `const t: (x: number) => Box<number> = local` pairs with an unrelated generic arrow `<T>(x: T) => new Cell(x)`. The program only uses that arrow with strings, and `Cell<number>` can't lower (its method narrows a field by `typeof`).
- The class instance is made inside a trial, which can't take class instances back, so `AllInstances` reports `PTS1001` and the build falls back.
- Effect: the unrelated interface call goes from 5 (base and Node) to TypeError, and the adapted line loses its instance too. The base lowers this program.
- Control `t04b`: replace the adapter with a plain typed call, `const b: Box<number> = local(1)`. The base then hits the same cliff, so this is pre-existing D-190 behaviour. What the slice adds is new kinds of demands (adapters, forwarders, keyed calls), each a new way into the cliff.
- Fix: record it in D-194's known gaps under C176/H09b5 with `t04` as the example. The real fix is H09b5's "only instances calls may use".

**R4-2 (low; coverage lost since round 3, output equals the base's): `namesResult` compares checker types by identity.**
- Where: `forwarding.go:384` and `:393` (`a == b`).
- Repro: `t01b_names_result_forms.pts`. The snap4r output is kept beside it as `.snap4r`. Three contexts that matched Node on snap4r now print the base's TypeError: an aliased union (`type MaybeBox = Box<number> | undefined`); an anonymous object result (`{ box: Box<T> }`); an async `then` callback (`Promise.resolve(10).then(later)`, where `later` returns `Promise<Box<T>>` and the context's result is `TResult | PromiseLike<TResult>`).
- These forms still match Node: a tuple, a readonly array, `Pair<Box<T>>`, `| null`, the async adapter to `Promise<Box<number>>`, `flatMap`, the plain `then` callback, and a context typed with an enclosing instance's type parameter.
- Fix: compare with the checker's identity relation instead of pointer equality, and unwrap `Promise`/`PromiseLike` for `then` callbacks. Or record the three forms with C215.

### What held
- **No lowering regressions:** `AllInstances`-only lowering of 1,465 programs shows nothing that lowers on the base failing on snap5r (57 more lower).
- **No output changes:** of 921 programs, 880 emit identical C, 27 fail on both trees, and 14 differ (the same 13 slice tests plus `church_combinators`). The three pre-existing ones with changed C print the same as the base and Node.
- **Tests:** all 14 match Node or their expect block, and all lower under `AllInstances` alone. The new lines fail on snap4r (the R3-2 lines in `generic_method_call_bind_this`; the `Cell` lines in `generic_values_keep_other_instances`). On the base, `generic_values_keep_other_instances` falls back in full: its keyed call becomes a value demand that pairs with the `Cell` arrow (`t03` shows the `Cell` part alone is fine on the base). So that guard is also red on the base, through the base's own C176 cliff.
- I did not re-run the matrices, since the parent is running them.

Key files: `/tmp/h09b3b/snap5r/pts/lower/forwarding.go`, `methods.go`.
