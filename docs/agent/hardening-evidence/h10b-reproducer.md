# H10b reproducer report (C279, C285, C274's rest)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree of `d365370a6`, 2026-10-24. Returned as its final message (children can't write report files) and saved here by the parent unchanged. Archive: [h10b-reproducer-probes.tar.gz](h10b-reproducer-probes.tar.gz) (generator, runner, cases, results, tests; no binaries).

---


All three defects reproduce on HEAD `d365370a6`. I built a new 748-case matrix and wrote 18 RED tests, all of which fail on HEAD. I also unskipped the four existing H10b tests (still RED) and wrote 5 guard tests, which pass.

- **Tests:** in my worktree's `pts/tests/run/`, nothing committed: 20 new untracked files, and the 4 existing tests modified only by deleting their `// skip:` line.
- **Archive:** `/tmp/h10b/h10b-reproducer-probes.tar.gz` (226 KB, no binaries); `/tmp/h10b/repro.done` is touched.
- **Worktree:** `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a429549311666c85c`. The five submodule directories are symlinks to the main checkout.
- **Machine budget:** no commits, stashes, pushes or ticks. One heavy job ran until `base-suite.done` appeared, at most two after it.

**Commands (all exit codes as expected):**
- `GOMAXPROCS=2 make build` (exit 0).
- The matrices: `PTS_HOME=<worktree> python3 -I bin/run.py CASES OUT.jsonl --jobs 1|2`. This is the archived runner with only the CLI path and work directory changed; Node runs as `node --no-warnings --experimental-transform-types` on an `.mts` copy, and each case's binary is deleted after it runs.
- The harness ran under `taskset -c 0,1`, because it sizes its job pool from `runtime.NumCPU()`.

## A. The earlier 777-case matrix on HEAD

Result: **622 OK, 130 TypeError, 25 PTS1001.** This is identical, case by case including the error details, to H10a's recorded `matrix/new10.jsonl`.

| Cause | Cases | Detail |
|---|---|---|
| C279 flows out of the narrowing | **129** | 128 TypeError and 1 PTS1001; breakdown below |
| C280 variance through `any` at the upcast | **24** | `var_wider_v`, `var_unknown_k`, `var_base_wider`, `var_class_arg` (6 each); see below |
| By design, D-070/D-072 | **2** | Two instances at one place |
| C285, C274's rest, new | 0 | The old matrix's private uses only read |

**C279 breakdown:**
- **Part A (100):**
  - `const_copy` 15
  - `array_literal` 21
  - `generic_call` 21
  - `return_inferred` 22
  - `par_map` 21
- **Part B:** `const_copy` 18.
- **Part C (11):**
  - `closure_const` 3.
  - `filter_inferred` 4, one of which is the PTS1001: `C_base_abstract__filter_inferred` gives ``using a `Option` as a `Some` ``.
  - `find` 4, with ``Expected MapLRU | undefined, got MapLRU<string, number>``.
- **Message:** `Uncaught TypeError: Expected MapLRU<any, any>, got MapLRU<string, number>` and the same for `Some`, `Flip`, `Sub`, `LeafCache` and `C`.
- **Which families:** `const_copy` passes for the uninferred families (`iface_noninfer`, `base_wrapped`, `base_nongeneric`, `error`, `object`, `empty`), because H10a holds those locals as dynamic values.

**C280:** all 24 errors are at the `probe(obj)` upcast line, which I checked. They are ``using a `MapLRU` as a `{get(string): number | string | undefined}` …`` and ``using a `Some` as a `Option` ``.

**D-070/D-072, two instances at one place:**
- `D_union_two_insts__field_write` gives `TypeError: Cannot set property val of MapLRU: expected string, got number`.
- `D_union_two_insts__pass_actual` gives `Expected MapLRU<string, number>, got MapLRU<number, string>`.

**Count discrepancy:** H10a's evidence counts "133 flows out, 20 variance". I find all 24 variance cases at the upcast, so mine is 129 / 24 / 2.

## B. The new H10b matrix (748 cases)

`bin/gen_h10b.py` generates the cases. Every use is wrapped in `try/catch` and prints a labelled line, so one binary reports every use. An `ERR <use>` line is a pts TypeError that Node doesn't throw.

**Sources (11):**
- `iface`, `gbase`, `pbase` (plain base class), `error`, `union`, `object`, `empty` (`{}`), `unknown`, `any`.
- `tparam`: S is inferred, so it is the class itself.
- `tparam_iface`: `S = Cache<string, V>`.

**Uses:**
- Basic: read (a field, a method and a getter), `hits++`, a push to `items`, `instanceof` again, a write.
- Strict: a pass to a `MapLRU<string, number>` parameter, and `===` with the original.

**Totals:** OK 134, TypeError 337, PTS1001 135, caught TypeError 18, silently wrong 124.

**F: flows out, basic uses (341).** Number elements, plus strings for 8 of the flows.
- **Every flow fails for `iface`, `gbase`, `union`, `unknown`, `any` and `tparam_iface`:** `const`, `let`, closure const, `[x]`, `{m: x}`, `ident`, inferred return, `C<any>` return, `find`, `filter`, `?:`, `??`, `await`, `.then`, `yield`, `new Map`, `new Set`, `[d] = [x]`, a `C<any>` parameter, field and array. They fail with a TypeError, or with PTS1001 for `{ m: x }` from `iface`, `gbase`, `union` and `tparam_iface`.
- **`pbase`, `error`, `object`, `empty`:** only `const`, `let`, closure const and `.then` are OK, because H10a makes those dynamic locals. All other flows throw a TypeError.
- **`member_next` / `member_next_const` (`let n = c.next`, `c.next!`):** OK for every source except the inferred `tparam`. There it gives `TypeError: The value narrowed to MapLRU is not an instance of it` (`pts_value_as_instance`, `pts/runtime/include/pts.h:936`, emitted by `pts/cgen/cgen.go:1122`). D-218's gap list is stale here: H10a already fixed `let n = c.next`.

**S: strict uses (88).**
- PTS1001 ``using a `MapLRU` as a `MapLRU` `` and `` the `===` operator on MapLRU and MapLRU `` everywhere, except:
  - the uninferred sources' dynamic locals, which pass;
  - `member_next`, which passes;
  - `await` from uninferred sources, which throws a TypeError.

**P: `.par` (33, threads 1, 2 and 4).** All throw a TypeError for every non-`tparam` source: `Expected MapLRU<any, any>, got …` or `Expected MapLRU[], got Array`.

**V: private uses (77).**
- **`direct` (C274's rest):** reads are OK. For uninferred, `unknown` and `any` sources, `pwrite` and `pcall` give `TypeError: Expected MapLRU<any, any>, got MapLRU<string, number>`. Inferred sources are OK.
- **After a flow:** everything throws. From uninferred sources, `const m2 = x; m2.#secret = v` gives `TypeError: Cannot add property #secret, object is not extensible`, and `m2.#bump()` gives `m2.#bump is not a function`. That is new; see the root causes.

**K: C285 container flows (160; sources `iface`, `gbase`, `pbase`, `object`, `unknown`, `any`).**
- Writes are lost silently (Node prints `[[8.5],[]]`, pts `[[],[]]`) for: spread, spread plus extra, `entries()`, `new Set`, Set `values().next()`, `Array.of`, `Array.from` of Map values, Map spread, `[...store.entries()]`, `new Map(store)`, `values().next().value`, `Promise.all`, and `Promise.resolve(row).then(f)`.
- `Array.from(c.matrix)`, `Object.values(c.rec)` and `Object.entries(c.rec)` are PTS1001 from `iface` and `gbase`: `object types with an index signature … (ArrayLike<any[]>)`. The same calls work in typed code without `instanceof` (probes q01 and q02).
- `c.pending.then(pushRow)` is PTS1001 from `iface` and `gbase` (H10a's by-reference callback rule) and silently wrong from the other sources.
- The controls `map((r) => r)`, `for…of` and `const [r0] =` are OK.

**E / ES / EP: explicit `MapLRU<any, any>` places, no `instanceof` (49).** Every one is PTS1001 ``using a `MapLRU` as a `MapLRU` ``, except `new MapLRU<any, any>(…)` itself, which is OK. The places are:
- a parameter, a default parameter, an annotated `let`, an `as` cast, `C<any>[]`, a return, a field;
- `Map<string, C<any>>`, a `Record`, a tuple, a generic argument, `C<any> | undefined`;
- a callback parameter, two instances at one parameter, and a reassignment.

**Two generator bugs, fixed and re-run** (`mb_fix_*`): unexpanded uses in the 30 `param_any` cases, and an unparenthesised `as` in `two_instances`. `tparam_iface` was added afterwards (49 cases). The merged results are `matrixH10b/mb_final.jsonl` and `mb_final_summary.txt`.

## C. RED tests

Each test fails on HEAD with the failure shown. Its `// expect:` block is Node's own output.

| Test | HEAD failure |
|---|---|
| `generic_instanceof_flows_out_locals` | PTS1001 at 39:20 ``using a `MapLRU` as a `MapLRU` `` (`{ m: c }`) |
| `generic_instanceof_flows_out_calls` | `TypeError: Expected MapLRU<any, any>, got MapLRU<string, number>` at :49 (`ident(c)`) |
| `generic_instanceof_flows_out_containers` | same TypeError at :41 (`[c, c]`) |
| `generic_instanceof_flows_out_predicates` | `Expected MapLRU \| undefined, got MapLRU<string, number>` at :39 (`find`) |
| `generic_instanceof_flows_out_sources` (generic base, plain base, Error, union, object, {}, unknown, any) | `Expected Some<any>, got Some<number>` at :103 |
| `generic_instanceof_flows_out_type_parameter` | PTS1001 `` `t.get` `` and `` `r?.get` is not supported yet ``; the `next` walk throws `The value narrowed to MapLRU is not an instance of it` |
| `generic_instanceof_flows_out_typed_uses` | PTS1001 ``using a `MapLRU` as a `MapLRU` `` (24:24, 29:35), `` the `===` operator on MapLRU and MapLRU `` (30:23) |
| `generic_instanceof_flows_out_par_sources` (threads 1 2 4) | `Expected Tagged[], got Array` at :37 |
| `generic_any_instance_places` | PTS1001 ``using a `Box` as a `Box` `` (8 sites) and `converting Box \| Box to Box` |
| `generic_any_instance_typed_uses` | PTS1001 ``using a `Box` as a `Box` `` |
| `generic_instanceof_private_dynamic_writes` (C274's rest) | `Expected Counter<any>, got Counter<number>` at :49 |
| `generic_instanceof_private_after_flow` | PTS1001 at 51:37 (`peekAny(s)`); the flows throw the TypeError |
| `generic_instanceof_nested_objects_dynamic_containers` | silent: `[ [], [] ] []` instead of `[ [ 1, 2, 4 ], [ 2, 4, 6, 7 ] ] [ 3, 5, 8 ]` |
| `generic_instanceof_nested_objects_into_more_containers` | PTS1001 `ArrayLike<any[]>` at 22:14, 23:17, 24:18 |
| `generic_instanceof_nested_objects_then_reference` | PTS1001 at 28:24 (`c.pending.then(pushRow)`); the first `.then` is lost silently |
| Unskipped `generic_instanceof_flows_out` | prints nothing (TypeError at the first flow) |
| Unskipped `generic_instanceof_unknown_flows_out` | prints only `read 41.5 41.5` |
| Unskipped `generic_instanceof_par` | prints nothing |
| Unskipped `generic_instanceof_nested_objects_into_containers` | silent: `[ [], [] ] []` |

There are no `errors/` tests: every case can have Node's output.

## D. Guard tests (pass on HEAD)

- `generic_any_instance_guard_new_any`: `new Box<any>`, `new Box(raw)`, `extends Box<any>`, `map`/`self`/`with` returning `Box<R>`, `this` and `Box<T>`, a `Box<any>` place, and a 200,000-iteration hot loop.
- `generic_any_instance_guard_library`: `Promise<any>`, `Map<any, any>`, `Set<any>`, `Array<any>`, untyped `new Map()` and `new Set()`.
- `generic_any_instance_guard_par` (threads 1 2 4): `.par` over `any[]` and over `Box<any>[]`.
- `generic_instanceof_guard_in_branch_sources`: H10a's in-branch uses from eight sources.
- `generic_instanceof_guard_next_members`: `let n = c.next`, `c.next!`, `c.byKey.get(k)?.val`.

**Existing tests that already guard `<any>`:**
- **Objects made for `any`:**
  - `generic_instanceof_any_instance_base`, `_any_instance_object_source`, `_any_instance_private_method`;
  - `generic_instanceof_generic_function_any_instance`, `_local_cache_order(_control)`;
  - `generic_methods_checks`;
  - `generic_class_instances_convert_{any_to_unknown,any_to_awaited,nonnullable_members,partial_members,erased_copy_results}` and `generic_class_instances_erased_copy_shared`.
- **Explicit `Box<any>` places:** `generic_dynamic_results_by_type` (`const anyBox: Box<any>`, `const kept: Box<any> = b`), `generic_dynamic_results_outer_type_arguments` (`Loader<any>`), `generic_values_keep_other_instances` (`as Cell<any>`).
- **No-diff tests that pin `Expected Box<number>, got Box<any>`:** this is the opposite direction, an erased copy's object going into a typed place. A place-only change should keep these: `generic_values_keep_other_instances`, `generic_value_forms_any_boundaries`, `generic_method_any_boundaries`, `generic_values_any_boundaries`, `generic_call_forms_any_boundaries`, `generic_polymorphic_recursion_past_the_depth`.
- **Library generics:** `untyped_new_map_contexts`, `logical_assign_typed_values`, `flat_cycles` (`new Array<any>`), `iterator_close_order` (`Promise<any>`), `array_pattern_*` and `iteration_unbounded_lazy` (`Generator<any>`).
- **`.par` over `any[]`:** `parallel_views_skip_holes`, `parallel_sort_holes`, `generic_instanceof_nested_objects_reads`.

## E. Harness on HEAD

`GOMAXPROCS=2 PTS_REQUIRE_NODE=1 taskset -c 0,1 go test -count=1 -timeout 40m -v -run '^(TestRun|TestRunMatchesNode|TestErrors)$/^(generic_instanceof_.*|generic_any_instance_.*)$' ./pts/tests/` exited 1, which is expected.

| Result | Count | What |
|---|---|---|
| PASS | 248 | All 5 guards in `TestRun` (and in `TestRunMatchesNode`, except `.par`), every earlier `generic_instanceof_*` test, and all `TestErrors` |
| FAIL | 42 | Exactly the RED set above: 18 new tests plus `generic_instanceof_par` in `TestRun`; 17 new tests plus 3 unskipped ones in `TestRunMatchesNode` |
| SKIP | 26 | Expected: no `// expect:` block in `TestRun`, or `.par` in `TestRunMatchesNode` |

`PTS_SMOKE_FILTER='generic_(instanceof|any_instance)_' PTS_SMOKE_MUTANTS=2 TestCompilerFailsCleanly` passed: 502 programs, no PTS9001. The RED tests can be skipped safely.

## Root causes (file:line)

1. **A typed narrowed reference flowing into a `C<any>` place:** `objects.go:966` leads to `classCast`. Its H10a case at `classes.go:1079` takes `downcast` (a checked `FromUnion`), which throws `Expected C<any…>, got C<X>`.
2. **A dynamic value flowing into a `C<any>` place:** `lower.go:1057` (`ir.Value` into a checkable class, a checked conversion). Only H10a's dynamic locals for uninferred sources escape it.
3. **Union and array places:**
   - A `C<any> | undefined` place takes the checked union path at `lower.go:1063`, which throws `Expected MapLRU | undefined, got …`.
   - A `C<any>[]` place takes `lower.go:1087` (dynamic-array conversion), which throws `Expected MapLRU[], got Array`.
4. **Explicit places:** `classes.go:1084` is the PTS1001 ``using a `Box` as a `Box` ``, because neither `C<X>` nor `C<any>` subclasses the other.
5. **`===`:** `lower.go:3043`. `commonClass` finds no common ancestor, so it is PTS1001.
6. **C274's rest:** `unions.go:137` and `exceptions.go:273` keep the downcast to `C<any,…>` for `isPrivateMemberChange` (`instanceof.go:758`).
7. **New: private names through dynamic locals.**
   - **Bug:** `dynamicKey` (`dynamic.go:126–129`) uses the private name's text.
   - **Writes:** they become `DynamicSet "#secret"` (`lower.go:3261`), which throws `Cannot add property #secret…`.
   - **Calls:** they become `dynamicMethodCall` (`dynamic.go:243`), which throws `m2.#bump is not a function`.
   - **Reads:** these work, because they go through `familyPrivateField` (`instanceof.go:640`).
8. **Type-parameter source:** `S & C<any>` copies fall to `` `t.get` is not supported yet `` at `lower.go:2767`.
9. **C285:** the rows are converted to tsc's `any[]` element type by `pts_value_convert` (`convert.c:1125`), which copies arrays.
10. **New, outside instanceof:** in typed code, a `number[]` that reaches an `any[]` through a promise is copied silently. Probes q05 and q06:
    - `Promise.resolve(rows[1]).then((r: any[]) => r.push(2))` loses the write; so does `.then(pushAny)` by reference.
    - `const p: Promise<any[]> = Promise.resolve(rows[3]); p.then(r => r.push(4))` loses the write.
    - The direct `const got: any[] = await q` is D-094's PTS1001, so only the promise paths escape the rule.
    - This blocks the `.then(f)` part of C285.
11. **Unrelated limitation, which I worked around in the library guard:** `const s: Set<any> = new Set([1, "1", true])` is PTS1001 (q04).

## Design observations

- **H10a already shows design C works for locals.** An uninferred source's dynamic local (`const m2 = x`) holds the object as a dynamic value. Reads, writes, pushes, a pass to a typed parameter (checked) and `===` all match Node; see the F and S rows for `pbase`, `error`, `object` and `{}`.
- **Extending design C.** Lowering every place tsc types `C<any…>` as `ir.Value`, including unions, arrays, Map/Set elements, parameters, fields and returns, would fix the TypeErrors, the PTS1001s in parts E and S, and `.par`. That covers `S & C<any>` too. Typed `C<X>` values box into these places without losing identity, and the checked conversion back to `C<X>` already exists.
- **Exceptions design C needs:**
  - `new C<any>(…)`, `extends C<any>`, `this` types and the library's own generics.
  - Tie-breaker for the hot loop: a `Box<any>` place in a hot loop becomes a dynamic get and set on every access. A typed fast path is worth keeping where a place provably holds only `C<any>` objects.
  - The pinned `got Box<any>` TypeErrors remain unchanged, since the objects keep class `C<any>`.
- **Private writes and calls:** they need a family-dispatched set and call, matching the `FamilyField` read. Otherwise they keep failing for dynamic places, both C274's rest and the new dynamic-local case in root cause 7.
- **C285:** containers built from such members need element types that hold the instance's own row objects as dynamic values, instead of converting them to `value[]`. For the inferred sources, `ArrayLike<any[]>` arguments need the same. The typed promise copy (root cause 10) needs a decision of its own: PTS1001 per D-094, or keep the object.
- **Type-changing writes through a `C<any>` place** (a string into a `Box<number>`'s field) will be D-070's checked TypeError. That should be documented.

Archive contents (all under `/tmp/h10b/h10b-reproducer-probes.tar.gz`):
- `bin/`: the generator, the runner, the analyzer, the Node and pts check helpers, and the harness and smoke scripts.
- `matrix777/`: the 777 results and their classification.
- `matrixH10b/`: the cases and every result file.
- `probes/` q01–q06.
- `tests/`: drafts, Node outputs, the final copies and the unskip patch.
- The harness, smoke and build logs.