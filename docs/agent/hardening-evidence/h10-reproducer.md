# H10 reproducer report: generic `instanceof` narrowing (C10)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree of `fe03221c0`, 2026-10-23. Returned as its final message (children can't write report files) and saved here by the parent unchanged. Archive: [h10-reproducer-probes.tar.gz](h10-reproducer-probes.tar.gz) (generator, runner, cases, per-design results, tests, prototypes' overlay sources; no binaries).

---

C10 reproduces on HEAD `fe03221c0`. A 777-case matrix compared with Node passes 230 cases on HEAD; the rest fail as TypeError, PTS1001 or, in 4 cases, silently wrong output. Neither candidate design is complete on its own. Values that flow out of the branch only work once tsc's `C<any, …>` is itself a dynamic value as a place type; I call that addition C. The A+B+C prototype passes 711 of 777 cases, breaks none of the cases HEAD passes, and passes the LRU corpus program. It still needs three dynamic-value bugs fixed (R1, R2, R4 below) before it is correct.

Everything ran in my worktree (`/home/bryan/projects/parallelscript/.claude/worktrees/agent-a3112385051b6fcf4`), at most 2 jobs at a time. I made no commits, stashes or pushes and ticked nothing. The worktree's only change is 19 untracked test files; the five submodules were initialised from local references and `make build` exited 0.

## 1. Re-reproducing C10

`pts/tests/corpus/compare.sh pts/tests/corpus/algorithms/lru_cache_map.pts docs/reviews/2026-10-13-final/grok/review-grok-probes/probes/17_instanceof_generic.pts docs/reviews/2026-10-13-final/gpt/review-gpt-probes/known-lru.pts` (script exit 0):
- **`lru_cache_map.pts`:** FAIL (Node exit 0, pts exit 1). Missing from `evicted: [ 'b', 'd' ]` onwards; stderr `Uncaught TypeError: Expected MapLRU<any, any>, got MapLRU<string, number>` at `:117`.
- **Grok `17_instanceof_generic.pts`:** FAIL. Node `hit 4`, pts `threw Expected MapLRU<any, any>, got MapLRU<string, number>`.
- **GPT `known-lru.pts`:** byte-identical to the corpus program; same failure at `:117`.

On HEAD the `unknown` source is only partly fine. Member reads work, but `const y = x` throws `Expected Box<any>, got Box<number>`.

## 2. The matrix

`/tmp/h10/matrix/gen.py OUT` generates complete programs; `run.py CASEDIR OUT.jsonl [--pts CLI]` runs Node exactly as the harness does and runs pts with a 30 s timeout. Binaries are deleted per case and 2 jobs run at a time. Generator bugs (NODEFAIL, TSERR) came to 0.
- **Part A (551 cases):** 24 source families × 23 uses, number elements.
- **Part B (96):** 6 element types × 4 families × 4 uses.
- **Part C (98):** 19 narrowing forms × 5 families, plus 3 error/catch forms.
- **Part D (32):** variance and two instances at one place.

**HEAD totals (`results_head.jsonl`, 777 cases):** OK 230, TYPEERR 297, PTS1001 246, WRONG 4. There was no PTS9001, CRASH or TIMEOUT.

| Source | OK of 23 | What fails |
|---|---|---|
| `iface*` families, `typeparam_iface` (explicit `S`) | 5 | 15 TYPEERR; PTS1001 on `pass_actual` and `return_declared` (`using a {get(string): number \| undefined} as a MapLRU`) and `private_peek` |
| Generic base classes (abstract, concrete, chain, wrapped) | 5 | 18 PTS1001: `using a Option as a Some`, `Base as Base`, `A as C`, `Bag as ListBag` |
| `Shape` base, `Error` base | 5–6 | 15 TYPEERR (`Expected Tagged<any>, got Tagged<number>`) |
| `object`, `{}` | 9 | field reads already dynamic; methods, getters and writes TYPEERR; `push_field` WRONG |
| `X \| undefined`, `X \| string`, union of two interfaces | 4–5 | ~18 PTS1001 (`converting undefined \| {…} to MapLRU`) |
| `typeparam_inferred` | 5 | 17 PTS1001 (`using a Other as a MapLRU`) in the dead branch for `S = Other` |
| `unknown`, `any` (guards) | 16–17 | flows out TYPEERR; `push_field` WRONG; `private_peek` PTS1001 |
| `multi_inst` | 0 | the second instance's own narrowing throws |
| Controls (same-class union, non-generic class) | 23 | none |

- **Part B:** every element type fails the same way. String, boolean, object, array, `Box<number>` and literal all throw for interface sources and are PTS1001 for the base-class source.
- **Part C:** `catch`, `catch (e: any)` and the explicit `filter` guard pass for every family. Every other form fails for interface, object and base sources. For `unknown`, the `property`, `this.cache`, `element`, closure-const, inferred-`filter` and `find` forms fail too.
- **Part D:** the variance sources are already PTS1001 at the upcast, before any narrowing (`using a MapLRU as a {get(string): number | string | undefined}…`). They are pre-existing and never reach H10's code.

## 3. RED and guard tests

All are in `pts/tests/run/`, with Node's output in each `// expect:` block. I left them unskipped.

Final HEAD run, pinned to 2 CPUs: `GOMAXPROCS=2 PTS_REQUIRE_NODE=1 taskset -c 0,1 go test -count=1 -timeout 30m -v -run '^(TestRun|TestRunMatchesNode)$/^(generic_instanceof_.*)$' ./pts/tests/` exited 1. The 15 RED tests fail in `TestRun`, and 14 of them in `TestRunMatchesNode` (`par` is skipped there as not plain TypeScript). The 4 guards and the 2 existing `generic_instanceof_map_*` tests pass.

`PTS_SMOKE_FILTER='generic_instanceof_' PTS_SMOKE_MUTANTS=4 go test -run '^TestCompilerFailsCleanly$'` passed: 105 programs compiled, no PTS9001.

Per-test results for every design (from `/tmp/h10/tests/check.py`):

| Test | HEAD failure | A | B | A+B | C | A+B+C |
|---|---|---|---|---|---|---|
| `interface_source` | TypeError | PASS | FAIL (R1) | PASS | FAIL (R1) | PASS |
| `base_class_source` | PTS1001 | PASS | PASS | PASS | PASS | PASS |
| `nongeneric_source` | TypeError | FAIL | PASS | PASS | PASS | PASS |
| `union_source` | PTS1001 | PASS | PASS | PASS | PASS | PASS |
| `flows_out` | PTS1001 | FAIL | FAIL | FAIL | FAIL | PASS |
| `unknown_flows_out` | TypeError | FAIL | FAIL | FAIL | PASS | PASS |
| `forms` | TypeError | FAIL | FAIL | FAIL | PASS | FAIL |
| `type_arguments` | TypeError | FAIL | FAIL | PASS | FAIL | PASS |
| `type_parameter_source` | PTS1001 | FAIL | FAIL | FAIL | FAIL | FAIL |
| `two_instances_one_place` | PTS1001 | FAIL | PASS | PASS | PASS | PASS |
| `own_class_methods` | PTS1001 (R4) | FAIL | FAIL | FAIL | FAIL | PASS |
| `new_own_instance_in_method` | PTS1001 (R4, no instanceof) | FAIL | FAIL | FAIL | FAIL | FAIL |
| `private_field_dynamic` | silent `undefined` (R2) | FAIL | FAIL | FAIL | FAIL | FAIL |
| `dynamic_member_writes` | silent lost writes (R1) | FAIL | FAIL | FAIL | FAIL | FAIL |
| `par` (`threads: 1 2 4`) | TypeError `Expected MapLRU[], got Array` | FAIL | FAIL | FAIL | PASS | PASS |
| Guards: `guard_dynamic_sources`, `guard_same_class`, `guard_nongeneric`, `guard_lying_predicate` (no-diff, D-149 TypeError) | pass on HEAD | PASS | PASS | PASS | PASS | PASS |

Two test edits to note:
- I took the else branch's `c.size` out of `interface_source`, because it hits R3 below rather than H10.
- In `nongeneric_source` I replaced `o.tag = o.tag + "!"` with `o.tag * 10`. A string can't go into a `Tagged<number>`'s typed field, so every dynamic design throws there; that's an honest divergence of the D-070 kind. The original is kept as `quick/typechange_write.pts`.

## 4. Root causes

tsc's narrowing (`getNarrowedTypeWorker`, checkDerived) keeps the declared type only when it is `C` or derives from it. A base class of `C` gives `C<any, …>`, because `C` derives from it. An interface, `object`, `{}` or `unknown` source relates in neither direction, so tsc falls back to the candidate, also `C<any, …>`. PTS keys a generic class's instances by their IR type arguments (generic_classes.go:172), so `C<any, …>` is a separate class with dynamic-value fields.

- **F1 (interface, non-generic base, `object`/`{}` source → TypeError).** The reference's declared type is `Cache<string, number>` (an IR interface); tsc's type there is `MapLRU<any, any>`.
  - Field read: `propertyAccess` lower.go:2652 → `membersAt` → `membersFor` → `receiverClass` (generic_classes.go:103/116, which uses `irType` of the any instance) → `fieldAccess` classes.go:1022 → `asInstanceOf` 1059 → `convert` lower.go:1067 → `objectCast` objects.go:968–969 → `isNarrowedTo` classes.go:1093 → `downcast` classes.go:1088, a checked `FromUnion` → the runtime type check fails.
  - Method calls take lower.go:1789 → `callMethod` classes.go:929 → the same path.
  - A non-generic base goes through `classCast` classes.go:1075.
  - `object`/`{}` sources are dynamic values but not `storedDynamic`. Their fields take the dynamic branch at classes.go:1026; their methods and writes go through lower.go:1047 (a checked conversion of a dynamic value).
- **F2 (generic base class → PTS1001).** Declared `Option<number>`, narrowed `Some<any>`. In `classCast` (classes.go:1070), `Some<any>` extends `Option<any>`, not `Option<number>`, so it reaches the PTS1001 at classes.go:1080. A field declared in the base gives "using a `Base` as a `Base`".
- **F3 (union source → PTS1001).** `narrowed` (unions.go:130): `isNarrowing` (105/116) is false because `MapLRU` is neither a member nor a subclass of one. `convert` has no union→class case and reports at lower.go:1086–1087. Two instances at one place (`Cache<string, number> | Cache<number, string>`) take the same path.
- **F4 (type-parameter source).** With `S = Cache`, F1 applies. With `S = Other`, `narrowedInstance` (generics.go:405) leaves `Other & MapLRU<any, any>` as inhabited, which ends in classes.go:1080. That branch can't run, since no object is both classes.
- **F5 (flows out of the branch, every source including `unknown`).** `const m2 = x`, `[x]`, `ident(x)` with `T = C<any>`, inferred returns, and the results of `filter`/`find` (TS 5.5 inferred predicates) all get places of the any instance's class. Converting the object into them throws or is PTS1001. From `unknown` it is the check at lower.go:1047.
- **R1 (silent lost writes; also outside generics).** `narrowedValue` exceptions.go:237 (the `isDynamicData` case) converts a typed `number[]` field to `any[]` through `pts_value_convert` (convert.c:1125, which copies). So `x.items.push(…)`, `x.items[0] = …` are lost and `x.items === x.items` is false. The same happens for `x: unknown` holding a `number[]` narrowed by `Array.isArray` (`quick/w2.pts`: Node `[ 10, 2, 3 ]`, pts `[ 1, 2 ]`).
- **R2 (silent `undefined`).** `#private` on a dynamic value: `dynamicKey` dynamic.go:126 looks up the key `"#secret"`, which no object has.
- **R3 (not H10).** A generic interface whose method mentions its type parameter, plus a `readonly size` provided by a generic class's getter, read through the interface: `TypeError: Cannot read property 'size' of #<K3>: it has none, though a type assertion says it has`. Minimal repro `quick/g6.pts`.
- **R4.** A new instance made in the middle of one of the class's own methods: generic_classes.go:198 → `ensureMembers` 549 (restores `subst`, `declaring` and `function`, but not `locals`) → `parameter` lower.go:1236–1238 rebinds the shared parameter symbols → `ownLocal` lower.go:3419 "using `o` from outside the function". This is `quick/p4.pts`, with no `instanceof`.
- **R5 (messages).** They show names without type arguments: "using a `Base` as a `Base`", "Expected MapLRU | undefined", "Expected MapLRU[], got Array".
- **R6.** `unknown`-typed properties and elements (`h.c`, `this.cache`, `arr[0]`) aren't `storedDynamic` (dynamic.go:54), so they get the typed path and throw.

## 5. Design evaluation

Prototypes are `go build -overlay` builds; their sources are archived and the worktree sources were not changed.
- **A** infers the class's type arguments from the declared type, structurally and through `extends`/`implements` (new `PtsInferClassInstance`), in `typeAt`, `receiverClass` and union `convert`.
- **B** keeps such a reference dynamic, via `storedDynamic` and a `box` in `narrowed`.
- **C** makes tsc's all-`any` instance an `ir.Value` as a place type, and `isAny` treats references of that type as `any`. Exceptions: `new`, superclasses, member tables, fluent `this`, and library classes (`ParallelArray<any>` in pts:parallel).

| Design | OK of 777 | WRONG | Breaks vs HEAD |
|---|---|---|---|
| HEAD | 230 | 4 | – |
| A | 496 | 4 | 0 |
| B | 543 | 22 | 0 |
| A+B | 571 | 7 | 0 |
| C alone | 631 | 42 | 0 |
| A+B+C | 711 | 14 | 0 |

- **A** fixes every in-branch use for inferable sources: the interface variants, base classes, unions, explicit `S`, multiple instances, and the corpus program and both probes. It leaves:
  - **Not inferable:** `Tagged<T, Extra>`, `Shape`, `Error`, `object`/`{}`, two instances at one place.
  - **Flows out:** these become compile-time PTS1001, because the place keeps tsc's `C<any>` (objects.go:968 false → classes.go:1080).
  - **The dead branch:** structural inference from the unrelated class `Other`.
  - **`any`-typed member expressions:** tsc still types them `any`, so `c.key + c.val` becomes PTS1001 "the `+` operator on string and number". A would have to retype every expression derived from the reference.
  - **Variance through `any`** (`quick/v1.pts`): `Expected MapLRU<string, string | number>, got MapLRU<string, number>`. That is safe but not what Node does. A literal argument of a type-computing class (`quick/v2.pts`) works.
- **B** fixes in-branch uses for every source. It is silently wrong wherever it writes through an array member (R1) or reads a private name (R2). Flows out still throw, exactly like the `unknown` sources on HEAD. It misses intersections (`isNarrowedDynamic` requires an object type, exceptions.go:271).
- **Flows out** fail under A (PTS1001) and B (TypeError); only C fixes them. C also fixes HEAD's PTS1001 for a `Box<number>` passed to an explicit `Box<any>` parameter (`quick/c1.pts`). C alone needs B's `box` hook and the R1 and R2 fixes. It also regresses `peekAny(o: Box<any>)`'s `o.#secret` from correct to `undefined` (R2).
- **Regression check:** A+B+C over every `TestRun`, `TestRunMatchesNode`, `TestCorpusMatchesNode` and `TestErrors` case, 2 CPUs, overlay. It fails only the 5 remaining RED tests (`regression_abc2.log`). The first attempt was invalid: a package-level flag raced between the harness's concurrent compilations, and C caught `.par` of `any[]`. Both are fixed in the archived sources.

**Recommendation:** C, plus A as a typed fast path where inference decides. A must leave intersections with an unrelated class to the dead-code rule, and must type the member expressions derived from the reference too. B's boxing hook is also needed, plus the prerequisite fixes for R1, R2 and R4. Type-changing writes through `C<any>` should be documented as a TypeError (D-070).

## 6. Files

All under `/tmp/h10`:
- **`repro-probes.tar.gz` (664 KB, no binaries):**
  - `matrix/`: generator, runner, `analyze.py`, `grid.py`, `designs.py`, `combined_csv.py`, cases, JSONL per design, `results_all_designs.csv`, `*.summary.txt`.
  - `tests/`: drafts, `final/` copies, harness and smoke logs, `check_*.txt`.
  - `quick/`: probes.
  - `protoA/`, `protoB/`, `protoC/`, `protoConly/`: overlay sources and regression logs.
- **Prototype CLIs (left in place for reproduction, about 240 MB):** `protoA/pts`, `protoB/pts`, `protoB/pts_ab`, `protoConly/pts_c`, `protoC/pts_abc`. Running them needs `PTS_HOME` set to the worktree.
- **`repro.done`:** touched as the last action.
