# H12 reproducer report (C12, C306, C307, ordering)

Child: `reproducer` (claude-opus-5-5, xhigh), isolated worktree of `9765ca1b6`, 2026-10-25. Returned as its final message (children can't write report files) and saved here by the parent below the rule, unchanged but for its title line. Archive: [h12-reproducer-probes.tar.gz](h12-reproducer-probes.tar.gz) (generator, runner, cases, results, tests, the prototype patch; no binaries).

---

I reproduced C12, C306, C307 and the ordering claim on the base CLI. A seeded 3,438-case matrix confirms that these defects and three related ones are much broader than the four named examples.

A prototype (`/tmp/h12/repro/h12-prototype.patch`, which touches `pts/lower` only) raises the Node-expected cases that pass from **2,484 to 3,091 of 3,255**, with **0 cases that passed before now failing**. The emit-C sweep shows that no existing program's generated C changes. The 164 cases still failing are pre-existing defects outside defaults (N1–N7). One limit you need to decide on: the C306 form that just does `return p` still throws `TypeError` with the prototype, because of the D-220 return rule (see N6).

Base CLI: `/tmp/h12/base` (main `9765ca1b6`). Node: `~/.local/node/bin/node --experimental-transform-types --no-warnings` on a `.mts` copy. I did not commit or push anything, and I did not touch your tree.

## 1. Reproduction of the named claims

Each program was run with `PTS_HOME=/tmp/h12/base /tmp/h12/base/pts-bin/pts run p.pts`. The full transcripts are in `results/direct-base.txt` and `results/direct-proto.txt`.

| Claim | Base (exit code) | Node | Prototype |
|---|---|---|---|
| C12 `const { value = 4 } = { value: undefined }` | 1, `1:9 PTS1001: converting undefined to number` | `4` | `4` |
| C12 other forms (`void 0`, declared `{a: undefined}`, assignment, whole-default parameter, function-local) | 1, PTS1001 `converting undefined to number/string` | match | match |
| C306 `p: string \| undefined = s.split(":")[1]`, then tests `p` | 1, `1:47 PTS1001: converting string \| undefined to string` | `b none given` | match |
| C307a `[a, b = "none"] = nums` | 1, `converting string to number` | `1 none` | match |
| C307b `[f = null] = flags` | 1, `converting null to boolean` | `null` | match |
| C307 nested tuple, review form `[[x = 7] = [undefined], y]` | 1, ``using a `[number \| undefined]` as a `[number \| undefined]` `` | `nested tuple default 7 s` | match |
| C307 nested tuple, prompt/parent form `[x, [y, z = 9]]` | **0, `1 x 9`: already passes on base** | `1 x 9` | match |
| C307 default before a rest (`[number, ...string[]]`) | 1, `using a (number \| string)[] as a string[] where it may be changed or kept` | `1 d []` | match |
| Ordering, parent's `order.pts` (getter typed exactly `undefined`) | 1, PTS1001 | `1 2 get a,d1,get b` | match |
| C297 guard `[a = 1] = [undefined]` | 0, `1 2` | `1 2` | match |

Two things the named examples don't show:
- **The default before a rest is not the cause of the rest-tuple failure.** `const [h2, ...rest2] = r` with no default fails the same way.
- **Ordering is already right on base when getters are typed `number | undefined`.** Only getters whose type is exactly `undefined` fail to compile.

## 2. Matrix results

The generator is `gen/gen.py --seed 12`; it writes the cases with an expectation per case.
- **Object cases (1,515):** 32 sources × 16 contexts × 10 default kinds, built from three full grids plus 250 seeded samples.
- **Array cases (1,555):** 31 sources, built the same way.
- **Other families:** nested patterns 258, evaluation order 40, C306 family 24, C307 family 28, binding types/generics/`int`/`u8` 18.
- **Dropped:** 616 annotated-parameter triples that tsc rejects (TS2322).

The runner is `tools/runner.py`. It builds a fresh work directory per case, deletes the binary afterwards, and caches Node results.

| Family | Expected | Cases | Base | Prototype |
|---|---|---:|---|---|
| obj | Node | 1451 | OK 1055, WRONG 60, TypeError 5, RangeError 85, PTS1001 246 | OK 1442, PTS1001 9 |
| arr | Node | 1440 | OK 1213, TypeError 18, PTS1001 209 | OK 1325, PTS1001 115 |
| nest | Node | 258 | OK 172, PTS1001 86 | OK 224, PTS1001 34 |
| order | Node | 39 | OK 22, WRONG 3, PTS1001 14 | OK 34, WRONG 3, PTS1001 2 |
| param | Node | 22 | OK 3, TypeError 2, RangeError 1, PTS1001 16 | OK 22 |
| c307 | Node | 28 | OK 6, TypeError 2, PTS1001 20 | OK 27, PTS1001 1 |
| bind | Node / no-node | 10 / 7 | OK 8+5, PTS1001 2+2 | OK 10+7 |
| arr / obj / param | by design | 55 / 2 / 2 | — | unchanged except 9 hole cases (see below) |
| catch, `for…of` assign, computed keys | out of scope | 124 | PTS1001 | PTS1001 |

How the "by design" rows were classified:
- **D-085:** `arr.num_arr_hole.*`. A number array has no holes; an unset slot reads `0`, so the default doesn't apply. Nine of these were PTS1001 on base and now compile to D-085's `0`.
- **D-070:** an `any` default (10) going into an annotated part typed `string`, `boolean` or an object is checked and throws `TypeError`.
- **D-090:** an untested parameter whose default reads an element (`p = xs[5]`) is typed `string` by tsc, so it throws `RangeError` past the end.

Base failures among the Node-expected cases, grouped by root cause (`results/analyze-base.txt`):

| Root cause | Cases |
|---|---:|
| RC1 exact-undefined part: PTS1001 | 211 |
| RC1 exact-undefined part: silent WRONG | 60 |
| RC1 exact-undefined part: nested pattern | 5 |
| RC2 default of another type over number/boolean arrays | 100 |
| RC6 record missing key | 85 |
| RC2b assignment-target typing | 74 |
| RC3 rest of a rest tuple | 26 |
| RC5 parameter defaults (C306) | 19 |
| RC3b assignment over a rest tuple | 14 |
| RC4 nested tuple-literal default | 8 |
| N2 array assignment over non-arrays | 117 |
| N1, N3–N7 | the rest |

Thirty nested cases fail at the source declaration itself (N7), before any default is involved.

## 3. RED and guard tests

All are in the worktree under `pts/tests/run/` (copies in `/tmp/h12/repro/tests/`). Each `// expect:` block comes from Node.

| File | Covers | Base | Prototype |
|---|---|---|---|
| `destructuring_defaults_exact_undefined.pts` | RC1: literal, `void 0`, declared, function-local, assignment, whole-default parameter, `for…of`, class field, generic instance, nested | PTS1001 | OK |
| `destructuring_defaults_exact_undefined_wide_names.pts` | RC1 silent: `any`/`unknown` defaults, an `any` target, a getter | **WRONG** (prints `undefined`) | OK |
| `destructuring_defaults_exact_undefined_order.pts` | getter read once and before its default, left to right, a default that changes the source, target evaluated before the read, a throwing default stops the rest | PTS1001 | OK |
| `array_pattern_defaults_other_types.pts` | RC2 / C307 a–b: string, null, object, `undefined` and union defaults; assignment, parameter, `for…of`, nested | PTS1001 | OK |
| `array_pattern_dynamic_defaults_over_numbers.pts` | RC2: `any`/`unknown` defaults over number/boolean arrays | TypeError | OK |
| `destructuring_default_assignment_targets.pts` | RC2b: `o.x`, `cells[0]`, `any` targets | PTS1001 | OK |
| `pattern_rest_of_rest_tuples.pts` | RC3 / C307 rest | PTS1001 | OK |
| `pattern_assignment_rest_tuples.pts` | RC3b | PTS1001 | OK |
| `pattern_nested_tuple_defaults.pts` | RC4 / C307 nested tuple | PTS1001 | OK |
| `parameter_defaults_may_be_undefined.pts` | RC5 / C306: split, element read, `maybe()`, `= undefined`, conditional, method `this.parts[i]`, arrow | PTS1001 | OK |
| `record_pattern_missing_keys.pts` | RC6 | RangeError | OK |
| `generator_parameter_defaults_at_call.pts` | N1 | WRONG | **WRONG** (the prototype doesn't fix it) |
| `destructuring_defaults_guards.pts` | guard: `null`/`0`/`""`/`false`/`NaN` don't take the default; union, holes, `[undefined]`, getter order, earlier names, parameters | OK | OK (identical C) |

There is also one new error test, `pts/tests/errors/array_assignment_patterns_not_yet.pts`. It pins N2's honest PTS1001 (generator, Set, `any`), and its messages are identical on base and prototype. Catch patterns and `for…of` without a declaration are already pinned by `errors/exceptions_not_yet.pts` and `errors/arrays_not_yet.pts`.

## 4. Root causes and recommended design

Line numbers are on base `9765ca1b6`.

- **RC1 (C12)** — `optional.go:79` `withDefaultCopying` returns the part as it is unless its type is a union with undefined. A part typed exactly `ir.Undefined` therefore reaches `bindName`, which fails with PTS1001, or, where the name accepts undefined, silently binds `undefined`. The tuple case (`destructuring.go:526`) already handles this.
  - **Fix:** use the default, converted to the target's type, but keep the part's read as `Sequence{part, default}`. Drop the read only when it is a pure read: a local, or a class or shape field of a local.
  - Don't drop a read that goes through an interface: `const src = { get a(){…} }` is held as an interface, and the read calls the getter. My first prototype dropped it and lost `get a` (caught by `order.getters_exact_undefined_literal_var`).
  - For a nested pattern, convert the default to tsc's type of that pattern.
- **RC2 (C307 a–b)** — `destructuring.go:588` `defaultPastTheEnd` converts the default to `array.Elem`. It should convert both branches to the name's type (tsc gives `number | string`). Same-type defaults keep the base IR exactly.
- **RC2b** — `optional.go:82`: for a non-identifier target (`o.x`, `arr[i]`), the default is typed as the part's type without undefined. Use the target's declared type instead. Related: `bindingType` on an assignment-target identifier reads the semantic pass's per-node (narrowed) type, so `let a: boolean | number; [b, a = …] = flags` fails. Use the variable's local type.
- **RC3 / RC3b (C307 rest)** — A rest tuple is held as an IR array of all its element types.
  - The rest's slice (`destructuring.go:540`) is a fresh array: copy it to the rest's type with a checked `dynamicConversion`.
  - Assignment targets (`destructuring.go:562`) should read the element dynamically and convert it with a check, as names already do.
- **RC4 (C307 nested)** — An array-literal default of a nested pattern is lowered with the contextual type tsc derives from the pattern, which gives a distinct tuple class (or `[value]`). Lower the literal directly as the part's tuple or array type (`tupleLiteral`). This is D-054's "own type, not the pattern's" rule applied to defaults.
- **RC5 (C306)** — `lower.go:1293` drops undefined from a defaulted parameter's type. `lower.go:1353` (`passedOrNot`) and `pts/check/functions.go:212` (`parameterNumeric`) do the same for unannotated parameters. `loose_locals.go:410` explicitly excludes parameter defaults. Two cases:
  - **The default's tsc type holds undefined** (`m.get(k)`, `o?.a`, `find`, `= undefined`, a conditional): keep the union, which is also tsc's body type. An `undefined` argument must still select the default.
  - **The default reads an element:** make the parameter a loose local (D-090/D-172 extended through `readsElement`), and read the default with `valueExpression`.
  - Either way D-220's sentence "a parameter's default isn't [a place undefined goes]" needs updating.
  - The prototype patches only the lowering side. The proper fix belongs in `parameterNumeric`, so that numeric types like `int` are kept.
- **RC6** — `destructuring.go:395` reads every record entry with `MapGetChecked`. Use `recordEntry` (D-172: undefined where the entry type holds undefined, with or without a default). Under a default, read with `MapGet` and let the default replace a missing key. This follows D-054's own reasoning that a default replaces the check.

Prototype evidence:
- `go test ./pts/lower/` passes.
- Focused `pts/tests` run (TestRun, TestRunMatchesNode, TestErrors; 4 CPUs via taskset): 492 pass, and the only failure is the generator RED test.
- emit-C sweep over 1,437 run/corpus/docs programs: only the 11 new tests' C changes.
- Costs (callgrind, loop only, base → prototype):

| Probe | Base | Prototype | Ratio |
|---|---:|---:|---:|
| Same-type default over `number[]` | 11.60M | 11.60M | 1.00× |
| Union property default | 56.30M | 56.30M | 1.00× |
| `v = m.get(k)` parameter, key present | 59.40M | 60.60M | 1.02× |
| `Record<string, number>` default, keys present | 97.0M | 119.4M | **1.23×** |

  The record cost could come down with a typed get-or-undefined map op.

## 5. New findings

All were reproduced in `nfind/` and all are pre-existing on base.
- **N1** — A generator binds its parameters (defaults and patterns) at the first `next()`, not at the call. The parameter prologue is prepended to the generator's frame body (`classes.go:769`). The order of effects is wrong (Node `default,created,body`, pts `created,default,body`), and a pattern error isn't thrown at the call (Node `threw at call TypeError`, pts `no throw at call`).
- **N2** — Array assignment patterns over a generator, Set, string, typed array or `any` are PTS1001 (`destructuring.go:564`; binding patterns have handled these since D-177). This is 117 matrix cases. Supporting them would also need the target evaluated before each `next()`.
- **N3** — A nested object-literal default in a `for…of` binding fails: `for (const { a: { b: { c = 3 } = {} } = {} } of [src])` gives PTS1001 ``using a `object2` as a `{b?: …}` ``. The `const` form works.
- **N4** — A generic default whose inferred type holds undefined fails for object-typed names: `{ a = d({ k: 5 }) }` gives PTS1001 `converting undefined | {k: number} to {k: number}`, in declarations and parameters. The number version works.
- **N5 / N7** — Literals in contexts holding undefined fail before any default is involved. Three forms: `{ a: [4] }` for `{ a?: (number | undefined)[] }`, `[[3], "s"]` for `[[number | undefined] | undefined, string]`, and `{ list: [{ v: 3 }] }`.
- **N6 (needs your decision)** — Your `c306.pts`, `function f(…, p: string | undefined = s.split(":")[1]) { return p; }`, throws `TypeError: Expected string, got undefined` with the prototype. tsc infers the result as `string`, so D-220's returned-value rule checks it. It matches Node with a `: string | undefined` result, or when the body prints or tests `p`. C306's "runs as Node" holds for the literal form only if the inferred result is widened; I recommend keeping the D-220 boundary and documenting it.
- Catch patterns and `for (x of …)` without a declaration remain honest, pinned PTS1001s and are out of H12's scope.

## 6. Commands to rerun

All commands are in `/tmp/h12/repro/rerun.sh`. Key ones:

```
python3 /tmp/h12/repro/gen/gen.py --seed 12 --out /tmp/h12/repro/cases2
python3 /tmp/h12/repro/tools/runner.py --cli /tmp/h12/base/pts-bin/pts --home /tmp/h12/base --cases /tmp/h12/repro/cases2 --out /tmp/h12/repro/results/matrix2-base.jsonl --jobs 4 --node-cache /tmp/h12/repro/results/node-cache.jsonl
python3 /tmp/h12/repro/tools/analyze.py /tmp/h12/repro/results/matrix2-proto-v3.jsonl --compare /tmp/h12/repro/results/matrix2-base.jsonl
python3 /tmp/h12/repro/tools/table.py /tmp/h12/repro/results/matrix2-base.jsonl /tmp/h12/repro/results/matrix2-proto-v3.jsonl
python3 /tmp/h12/repro/tools/emitc_sweep.py /tmp/h12/repro/results/emitc-sweep.json --jobs 4
```

The prototype CLI is built from the patched worktree with `go build -o /tmp/h12/repro/proto/pts-bin/pts ./cmd/pts` and uses `PTS_HOME=/tmp/h12/base`.

The worktree's `pts/third_party/*` are symlinked to the main checkout, so they show in `git status` as submodule changes. An empty leftover `work/direct.*` directory remains outside the archive.

Files:
- `/tmp/h12/repro/h12-reproducer-probes.tar.gz` (no binaries)
- `/tmp/h12/repro/h12-prototype.patch`
- `/tmp/h12/repro/rerun.sh`
- `/tmp/h12/repro/results/` (`matrix2-base.jsonl`, `matrix2-proto-v3.jsonl`, `table-base-vs-proto-v3.md`)
- In the worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a4625cf9703fcd106`:
  - 13 tests under `pts/tests/run/`
  - `pts/tests/errors/array_assignment_patterns_not_yet.pts`
  - the uncommitted prototype edits in `pts/lower/{optional,destructuring,lower,loose_locals}.go`