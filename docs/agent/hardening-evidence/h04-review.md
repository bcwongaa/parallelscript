# H04 independent review

- **Reviewer:** the named `opus-reviewer` subagent (Agent call with the `opus` alias, which the session's environment maps to claude-opus-5-5; effort xhigh), read-only on the parent's tree, building its own CLI from it into `/tmp/pts-h04-review` and comparing with the base CLI (`PTS_HOME=/tmp/h04-base`) and Node (`.mts` copies). Three rounds on 2026-10-16/17, the second and third continuing the first's context.
- **Probes:** [h04-review-probes.tar.gz](h04-review-probes.tar.gz) holds the reviewer's three archives as it made them (`h04-review-probes.tar.gz`, `h04-review2-probes.tar.gz`, `h04-review3-probes.tar.gz`: `p/`, `p2/`, `p3/` with each program's new, base and Node outputs, the scripts, the fake C compiler, the overlay mutants and the logs).
- **Parent's handling:** every finding was re-run by the parent before it was fixed; the fixes and their tests are in [h04.md](h04.md) (Review). The reports follow unchanged.

---

## Round 1

**Verdict: NOT READY.** Containment works at every CLI entry I tried. But the C39 change to `narrowedValue` breaks valid programs that ran correctly on the base, some of them silently (finding 1).

Repro tool for the run findings: `/tmp/pts-h04-review/scripts/cmp.sh <file.pts>` runs the new CLI (`PTS_HOME=/home/bryan/projects/parallelscript /tmp/pts-h04-review/pts run`), the base CLI (`PTS_HOME=/tmp/h04-base`) and Node (`.mts` copy) and diffs their output.

### Findings (all verified by running unless marked as a suspicion)

**1. Blocker: regression from correct output to wrong output or a TypeError.** `pts/lower/exceptions.go:230-239` now converts a value that is declared `any`/`unknown` and narrowed by a user type guard or an `asserts` guard to an array, tuple, record, Map or Set type. On the base, method calls on these values were dispatched dynamically and matched Node.
- `p/c/c03_any_push_only.pts`:
  ```ts
  function isNums(x: any): x is number[] { return Array.isArray(x); }
  const j: any = JSON.parse("[1,2]");
  if (isNums(j)) { j.push(3); }
  console.log(j);
  ```
  Base and Node print `[ 1, 2, 3 ]`; new prints `[ 1, 2 ]`, because the push lands on a copy.
- The same loss happens with `unknown`:
  - `p/d/d01_unknown_push_only.pts`: a `push` after `x is string[]`.
  - `p/d/d03_param_guard_sort.pts`: `x.sort()` inside a function taking `x: unknown`.
  - `p/u/u02_asserts.pts`: `asserts x is number[]`, then `push` and `sort`.
- `p/c/c01_any_set_guard.pts`:
  ```ts
  function isStrSet(x: unknown): x is Set<string> { return x instanceof Set; }
  const s: any = new Set(); s.add("a");
  if (isStrSet(s)) console.log(s.has("a"), s.size);
  ```
  Base and Node print `true 1`; new throws `TypeError: Expected Set<string>, got Set`. `p/c/c02_any_map_guard.pts` fails the same way for `Map<string, number>`. `isDynamicCollection` only exempts `Map<any, any>` and `Set<any>`.
- `run/narrowed_dynamic_data_copies.pts` pins this behaviour as a "documented gap". Its `push` line worked on the base, so the pin enshrines a regression.

**2. Should-fix: costs and semantics that come from the same conversion.** The base panicked on these, so they are not regressions, but they fail silently.
- **Quadratic cost.** `p/e/e01_quadratic.pts` sums a 20,000-element `unknown` array narrowed by a guard; the binary takes 27.6 s. Every `data.length` and `data[i]` read copies the whole array.
- **Identity.** `parsed === parsed` is `false`.
- **Object literals are copied too.** `p/d/d04_obj_literal_record_write.pts`: the program's own object literal held as `unknown` and narrowed to `Record<string, number>` is copied, so the write `c.a = 10` is lost (Node prints `{ a: 10, b: 2 }`). The claim that program objects are kept doesn't hold for shapes narrowed to records.

**3. Should-fix: environment failures reported as a bug in pts (exit 3).**
- **Linker.** `pts/driver/driver.go:87,105` treats every `undefined reference` as the generated C's fault. Repro: a fake C compiler that drops the libuv archive, `FAKE_MODE=dropuv PTS_CC=/tmp/pts-h04-review/cc/fakecc … pts build cc1.pts`. It prints `internal compiler error: the generated C doesn't link: undefined reference to 'uv_err_name'`, yet the reference comes from `libptsrt.a(007-error.o)` / `pts/runtime/src/error.c:345`. Suggested fix: only count references whose location isn't an archive member.
- **Version skew.** The new CLI with a `PTS_HOME` from commit `25448fc5d` (`/tmp/pts-h04-review/skew`, rebuilt with `git archive`) reports `skew.pts:2 - error PTS9001 … call to undeclared function 'pts_iteration_open'` and "This is a bug in pts".
- Other toolchain failures I tried (clang killed, a missing header's `fatal error`, clang crashing out of memory, an unwritable output path) correctly stay at exit 1.

**4. Should-fix: the object-literal accessor shape fix is incomplete.** `p/n/n03_min.pts`:
```ts
let backing = 1;
const ro = { get v() { return backing; } };
const rw = { get v() { return backing; }, set v(x: number) { backing = x; } };
const list = [ro, rw];
const anyRo: any = ro;
try { anyRo.v = 4; console.log("no throw", ro.v); } catch (e) { console.log("threw", (e as Error).constructor.name); }
```
New: exit 3 with `error PTS9001: internal compiler error while emitting C: … nil pointer dereference` at `pts/cgen/classes.go:440` (`shapeFieldEntries`; a nil field in the property order), with no position. Base panicked at `lower.fieldFuncType`. Node prints `threw TypeError`.

**5. Should-fix: a generic tuple rest gives a runtime TypeError.** `p/s14.pts`:
```ts
interface Pairs { pair<T>(...items: [T, T]): T[] }
class P implements Pairs { pair<T>(...items: [T, T]): T[] { return items; } }
const p: Pairs = new P();
console.log(p.pair(1, 2));
```
New throws `TypeError: Expected number[], got Array`; base panicked; Node prints `[ 1, 2 ]`. The parent's own `hunt-after.txt` lists it as WRONG, but the D-179 draft doesn't list it as a known gap. The non-generic form gives an honest PTS1001.

**6. Should-fix (test quality): the position-restore logic is never tested.** I mutated `pts/lower/lower.go:399,1329` and `pts/check/check.go:107` (dropped the `current`/`at = outer` restores) via `go test -overlay /tmp/pts-h04-review/overlay/{norestore,check_norestore}.json`. Every containment test in lower, driver, cmd/pts, check and lsp stays green. The injected failure fires on the node it has just set, so the reported position is correct by construction. A test where the failure happens after a child node has been lowered or visited would catch a missing restore.

**7. Nit: places that report no position, found by reading the code.**
- `pts/check/check.go:71-72`: `unsupportedFeatures` and `parallelBodies` (the parallel guardrail) never set `c.at`, so a panic there has no position.
- cgen's collect and header phases have none either (finding 4 shows this).
- Suspicion: `out.at` ignores positions with `Line == 0` (`cgen.go:1800`), so a synthetic function could report the previous function's line.

**8. Nit: `pts test` exits 1 when a file hits an internal error** (`cmd/pts/test.go:169`, pinned by `TestTestReportsATestFileTheCompilerFailsOn`). The usage text and D-179 suggest 3. Either document it or exit 3.

**9. Nit: a getter can still run twice.** `p/l/l04_getter_nonfunction.pts`: when an `@@iterator` getter returns a non-function, it runs twice before the TypeError; Node runs it once.

**10. Nit: stale or missing docs.**
- The `pts/lsp/lsp.go:160` comment still says the warning goes "at the start of file".
- The D-179 draft still mentions three getter reads and `assignedMember`, which isn't in the code.
- The exact C50 program now prints `1 {}` where Node prints `1 { b: 2 }`. That follows SPEC §8 / D-058, but no `no-diff` test pins this class-instance-through-interface case.

### Verified working
- **Unit tests.** The focused packages pass: `pts/diagnostic`, `pts/driver`, `pts/check`, `pts/lower`, `pts/cgen`, `pts/lsp` and `cmd/pts` (`unit.log`).
- **New harness tests.** The 13 new run tests pass `TestRun` and `TestRunMatchesNode`, and `TestErrors/symbol_iterator_element_writes` passes. The parent's RED records show the right reasons on the base.
- **CLI with a real internal error** (`p/t/`, using finding 4's program):
  - `build`, `run` and `emit-c` exit 3 with the message format described.
  - Concurrent `pts test` reports the file as "didn't compile" and still runs the other files.
  - A clang rejection maps to the `.pts` line, and `PTS_DEBUG=1` shows clang's command and output.
- **Iterator runtime change.** Getters are read once, `this` is bound for methods, async iterators work, and Map/Set/array/string/generator/typed-array iteration through `any` is unchanged (k01–k04, l02, l03).
- **Destructuring and spreads.** Rest in destructuring assignments, including into a property target, with defaults and nested patterns, works (m01–m05). Empty spreads and `Object.assign` work.
- **Tuple rest parameters.** Interface methods with tuple rests work, including fewer implementing parameters, named, optional and readonly tuples, and spread calls; a union of tuples gives an honest PTS1001.
- **Assignment values.** Identity holds for `(o.p = v) === o.p`. These all match Node: compound `+=` on `any`, `??=`/`||=`/`&&=` on `any`, erased generic code, `{}`/`object` places, chained assignments, for-loop initialisers and many right-side types.
- **Generated C across the existing suite.** I emitted C for 687 run/ and corpus programs with both compilers: 668 are byte-identical and 7 differ. The differences:
  - extra checks on chained assignments through union places (a microbenchmark is faster, not slower);
  - a simpler spread in `writer_logging`;
  - `||=` converting before its truthiness test.

  The affected tests still pass and match Node.
- **Smoke harness.** At its defaults it compiles 1,634 programs in 93 s on 2 jobs, peaking at 1.4 GB, and passes. It is a reasonable regression net, but it doesn't catch findings 1, 4 or 5, and it doesn't run clang by default.

### Probes
Everything is in `/tmp/pts-h04-review/` and archived in `/tmp/pts-h04-review/h04-review-probes.tar.gz`:
- `p/{a,b,c,d,e,f,h,i,j,k,l,m,n,q,r,s,t,u,v,w,x,y,z}/*.pts`: 100 programs, each with its new, base and Node outputs.
- `scripts/cmp.sh` and `scripts/cdiff.sh`.
- `cc/fakecc`: the fake C compiler with modes `dropuv`, `badc`, `killed`, `fatal`, `oom` and `readonly`.
- `overlay/`: the restore mutants.
- `cdiff/summary.txt`, `smoke.log` and `unit.log`.
- `skew/`: rebuild with `git archive 25448fc5d pts/runtime`, plus a symlink to `pts/third_party`.

I made no edits to the parent's tree (its only new changes are its own doc edits).

---

## Round 2

**Round 2 verdict: NOT READY.** Every round-1 fix checks out, but the new dynamic routing for narrowed values has opened a new regression: valid programs that ran correctly on the base now stop with an internal compiler error (finding R1).

How I checked: I rebuilt the CLI from the current tree and re-ran all round-1 probes (new vs base vs Node), plus 71 new probes. Command for every repro below: `/tmp/pts-h04-review/scripts/classify.sh <file.pts>`. It runs new (`PTS_HOME=/home/bryan/projects/parallelscript /tmp/pts-h04-review/pts run`), base (`PTS_HOME=/tmp/h04-base`) and Node, and tags `REGRESSION` when the base matched Node and new doesn't.

### Round-1 fixes (all verified by running)
- **1:** c01, c02, c03, d01, d03 and u02 now match Node. Across the whole round-1 set, no probe regresses against the base (`r2-classify.txt`).
- **2:** e01 runs in 0.01 s (it was 27.6 s).
- **3:** the dropped-libuv case now exits 1, not 3. A real clang rejection (`badc`) still exits 3 at `cc1.pts:2`. `fatal error` and a killed compiler stay at exit 1.
- **4:** n01, n03_min and n04 match Node.
- **6:** my no-restore overlay mutants now fail the new `TestAPanicAfter…` tests in `pts/lower` (2 tests) and `pts/check` (1 test).
- **7:** the round-1 cgen internal error now reports `ice.pts:3`.
- **9:** l04 matches Node; `run/iterator_getter_not_a_function.pts` passes.
- **10:** both pins are in place.
- **Tests:** the unit packages pass (diagnostic, driver, check, lower, cgen, lsp, cmd/pts). All 18 relevant run tests pass `TestRun` and `TestRunMatchesNode` (the two no-diff pins skip, as intended). `TestErrors/dynamic_delete` and `symbol_iterator_element_writes` pass.
- **Smoke harness:** passes, 1,642 programs in 152 s at 2 jobs (the host is also running your suite), 1.4 GB peak.
- **Codegen across the existing suite:** of 715 run/ and corpus programs, 669 emit byte-identical C to the base. The 7 that differ are the round-1 set minus writer_logging, plus the new pin. No program that compiled on the base fails now.

### Findings

**R1. Blocker (verified): results of method calls on narrowed dynamic values are wrongly typed, a regression from the base.** When the receiver is a dynamic value narrowed to an array or tuple, the result of a method like `slice` is now a dynamic value that tsc still types as an array or string. `.length`, `[i]` or string members on that result then panic the C emitter, or produce C that clang rejects. On the base these programs ran and matched Node. Containment works (exit 3, with a position), but the programs no longer compile.
- Template, in `p2/g/g01.pts`:
  ```ts
  function isNums(x: unknown): x is number[] { return Array.isArray(x); }
  const u: unknown = JSON.parse("[3, 1, 2]");
  if (isNums(u)) console.log(u.slice(1).length);
  ```
  New: `g01.pts:3 - error PTS9001: internal compiler error while emitting C: interface conversion: ir.Type is *ir.Primitive, not *ir.Array` (`cgen/arrays.go:12`). Base and Node print `2`.
- The same failure, all regressions from the base:
  - `u.filter(…)[0]`, `u.concat([9]).length`, `u.splice(0, 1).length`
  - `u.slice(1)[0] + 1` (fails while lowering)
  - `u.reverse().length`, `u.sort()[0]`, `u.fill(0).length`
  - `u.map(…).length`, `u.map(…)[0]`, `u.flat().length`
  - `u.join("").length` and `u.toString().length`, which give clang's `member reference type 'pts_value' … is not a pointer`
  - The same pattern through other narrowings: an `any` declaration (`i01`), a catch variable (`i02`), an `asserts` guard (`i03`) and a tuple (`i07`).
  - f01, a realistic JSON-validation program with `raw.filter(…).length`.
- These work: `for…of` over the result, spread, destructuring, chained `.map(…).map(…)`, `.indexOf`, `Math.max(...)`, number results (`at`, `pop`, `find`), and binding to a typed local.
- The new `run/narrowed_dynamic_values.pts` only passes such results straight to `console.log`, so it doesn't catch this.
- A likely fix: treat a call whose callee is a member of a narrowed dynamic value as `storedDynamic` (so its members go through `narrowedMember`), or narrow `dynamicCall`'s result to tsc's type.
- A related case that the base also panicked on: `m.get("a")!.length` on a guard-narrowed `Map<string, number[]>` is now an internal error (`i04`).

**R2. Should-fix (verified, not a regression; the base panicked): missing elements and keys throw.** `narrowedMember` checks a member read strictly against tsc's member type.
- `u[5]`, `u[5] === undefined` and `u[5] ?? "none"` on a narrowed `number[]` throw `TypeError: Expected number, got undefined` (`p2/k/k01`). So does `r.zz` on a narrowed `Record<string, number>` (`k02`), and the `while (u[i] !== undefined)` bounds idiom (`k03`).
- Node gives `undefined`, and so does pts's own typed code for the same reads: `typed[5]` and `typedRec.zz` print `undefined true` (D-015).

**R3. Info: pre-existing (same on the base), outside the slice, worth a known-issues line.**
- `Array.isArray(u)` where `u: unknown` or `any` holds the program's own typed array (`[1, 2]` or `[1, "q"]`) converts it to `any[]` by copying, so `u.push(3)` is silently lost (`p2/j/j05`, j01). D-179's "the array itself" holds only for JSON data.
- An `unknown` narrowed to an interface (`x is Config`, `f02`) loses nested writes: `raw.hosts.push`, `raw.limits.b = 2`.
- `forEach` on a dynamic `Map` narrowed to `Map<string, number>` throws (`c01`), on the base too.
- Binding a narrowed value to an unannotated local (`const list = items`) copies, which is the documented D-071 behaviour.

**Performance (verified, no regressions):**
- Class narrowing of `unknown` keeps typed field access: 166 ms vs 130 ms for a typed local (base 226/142).
- A guard-narrowed loop over 10 million elements takes 509 ms. A plain `any` loop takes about 770 ms on both base and new, and Node 46 ms. So this is the normal dynamic cost.
- A JSON-heavy `any` program matches Node, and its C is unchanged from the base.

### Probes
Everything is in `/tmp/pts-h04-review/`, archived as `h04-review2-probes.tar.gz`:
- `p2/{a,b,c,d,e,f,g,h,i,j,k,m,t}/*.pts`: 71 programs, each with its new, base and Node outputs.
- `r2-classify.txt` (round-1 probes on the new CLI), `cdiff2/summary.txt`, `unit2.log`, `smoke2.log`.
- `overlay/{norestore2,check_norestore2}.json` (the mutants), `scripts/classify.sh`, `scripts/cdiff2.sh`, and `r2.diff` (the tree's diff as I reviewed it).

I made no edits to the parent's tree.

---

## Round 3

**Round 3 verdict: READY.** Both round-2 findings are fixed. I found no new internal compiler error, and no program that matched Node on the base and now differs, either in the earlier probes or in 34 new probes aimed at the interface and method-result routing. Only one nit remains.

I rebuilt the CLI from the current tree. Repro command for every probe: `/tmp/pts-h04-review/scripts/classify.sh <file.pts>`, which compares new, base and Node and tags a `REGRESSION` when the base matched Node and new doesn't.

### Verified
- **All earlier probes:** I re-ran the 139 round-1 and round-2 probes.
  - **Clean:** 0 regressions against the base and 0 internal errors.
  - **Round 1:** every result is unchanged from round 2.
  - **Still differing from Node:** 12 round-2 probes. Each is a documented gap or an honest error the base gives too:
    - the copy at a typed local or a callback's typed parameter (f01, b04, k04);
    - `Array.isArray` copying the program's own typed array (j01, j05);
    - honest PTS1001s for unions, intersections, `in` and dynamic `delete`;
    - a dynamic `Map`'s `forEach` throwing, which happens on the base too (b05, c01).
- **R1 (method results) fixed:** the 18 regressions all match Node now. That covers g01/g03/g04/g09, h09–h12, i01–i03, i07, m01–m03 and m05/m06. i04 (`m.get("a")!.length`) also matches.
- **R2 (missing elements and keys) fixed:** k01–k03 match Node. Contexts that accept `undefined` give `undefined`:
  - a typed local, a later assignment, a parameter, a template literal, `push` into `(number | undefined)[]`, `==`/`typeof`, an arrow's return value, an optional property, and `String()`.

  Array literals, object literals and `u[5] + 1` throw, which is what pts's own typed arrays do there (D-015).
- **Interface routing (new probes in `p3/b` and `p3/c`):** all match Node:
  - interface methods on a class instance and on an object literal held as `unknown`, including writes followed by method calls;
  - optional and nested interfaces from JSON, with deep writes (`raw.friends[0].friends.push`, `raw.addr!.zip = …`) and `typeof` on optional members;
  - passing members to typed functions;
  - `for…in`, `Object.entries`/`keys`/`values`, and writes to index-signature members;
  - `instanceof` and `typeof` on members, including a class member's typed method;
  - generic guards (`isBox<T>`), in generic functions as well;
  - union-typed members narrowed by `in`, and `??=` on an optional array.

  The two exceptions match the base and are documented behaviour: a fixed-layout write that changes a property's type (b05), and a copy at an inferred typed local (b03).
- **Lib types:** narrowing `unknown`/`any` by `instanceof` to `Error`, `RangeError`, an `Error` subclass, `Date`, `RegExp`, `Promise` and typed arrays still uses the typed members and matches Node (`p3/a`).
- **Numeric types:** a guard-narrowed `{ x: i32; alpha: u8 }` from JSON with out-of-range values now throws a TypeError. That is the same check a direct typed conversion of the same JSON already makes; the base silently truncated to `1` and `255` (Node prints `1.5` and `300`). This is a behaviour change, but toward the documented boundary rule.
- **Performance:** a hot loop on an `unknown` holding the program's own object narrowed to an interface takes about 350 ms on new and 360–480 ms on base. A loop over a JSON array of interfaces takes about 880 ms on both. So the interface routing adds no cost.
- **Tests:**
  - The focused unit packages pass (diagnostic, driver, check, lower, cgen, lsp, cmd/pts).
  - All 18 relevant run tests pass `TestRun` and `TestRunMatchesNode` (the two no-diff pins skip, as intended). `TestErrors` passes for `dynamic_delete` and `symbol_iterator_element_writes`.
  - `TestCompilerFailsCleanly` passes: 1,642 programs in 152 s at 2 jobs.

### Finding
1. **Nit (verified).** Reading past the end of a narrowed dynamic array in a checked position throws `TypeError: Expected number, got undefined`. A typed array in the same position throws D-015's `RangeError: Array index 5 is out of range (length 2); if it may be missing, read it with ?? fallback…`. The failing positions are the same, but the message and error class differ and the hint is lost (`p3/e/x05.pts` and `x06.pts` against `y01.pts` and `y02.pts`). This doesn't block.

Probes are in `/tmp/pts-h04-review/p3/{a,b,c,d,e}/*.pts` (34 programs, with outputs), with the re-run results in `r3-classify.txt` and `r3-tests.log`. Everything is archived in `/tmp/pts-h04-review/h04-review3-probes.tar.gz`. I made no edits to the parent's tree.
