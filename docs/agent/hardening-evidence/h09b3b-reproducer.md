# H09b3b reproducer report

Saved by the parent from the `reproducer` subagent's final message (claude-opus-5-5, xhigh; the Agent call passed the `opus` alias), 2026-10-20. The subagent ran in its own worktree `.claude/worktrees/agent-abc741676604e62de` of `a9cbf8ac3` and committed nothing. Its archive is [h09b3b-reproducer-probes.tar.gz](h09b3b-reproducer-probes.tar.gz) (sha256 `dffac1fdda44bf13d587678e4bd92abef1d651085c41fab101bdbb4de97227ac`; `INDEX.txt` inside). The parent re-ran its probes, matrix and proposed tests itself; see [h09b3b.md](h09b3b.md).

---

I reproduced all five families on HEAD `a9cbf8ac3` and wrote seven RED tests and one no-diff guard. Three things outside the planned scope turned up:
- **A silent wrong `this` (pre-existing):** `a.make.call(b, …)`, `.bind(b)` and `.apply(b, …)` on a class's generic method run the method on `a`, not `b`.
- **A compiler crash (pre-existing):** reading an abstract (dispatched) generic method as a value fails with PTS9001 in every context.
- **C205 has a different cause than recorded:** the suspension does not keep the method as a value. A `yield`'s resumed value is a dynamic value, and D-191 never passes a dynamic value to an instance.

I also wrote two prototypes. One fixes C205; the other fixes most of family 3. Neither has been reviewed and neither is a proposal of record. Nothing was committed; the worktree is clean at `a9cbf8ac3`. The archive is `/tmp/h09b3b/repro/h09b3b-reproducer-probes.tar.gz` (sha256 `dffac1fd…227ac`), with `/tmp/h09b3b/repro/DONE` touched last.

## Setup
- I initialised the submodules with `--reference` and ran `GOMAXPROCS=2 make build` (exit 0). The CLI sha256 is `b1114e27…d906`.
- Node is v24.21.0, run the harness way: an `.mts` copy with `--no-warnings --experimental-transform-types`.
- Probes run with `bash tools/tri.sh probes/*.pts`; raw output is in `out/head_all_probes.txt`.
- Matrices and suites ran at 2 jobs, never more than 2 heavy jobs at once.

## Step 1: the H09 754-case matrix on HEAD
`h09m/results-head.json`: 562 OK / 116 TYPEERR / 74 TSERR / 2 DIAG.
- The 74 TSERRs are the generator's `.call`/`.apply` cases, which tsc types `Box<unknown>`.
- Against H09b1's round-6 snapshot (556 OK / 122 TYPEERR, copied to `h09m/results-h09b1-snap8.json`), six cases moved TYPEERR → OK (the `map__iface_method__*` and `map__object_method__*` variants). Nothing got worse.

| Family | Result |
|---|---|
| `bind` | 35 TYPEERR, 2 DIAG (PTS1001, object type method with another `this`) |
| `map` context | 29 TYPEERR, 8 OK |
| Class method values | 6 TYPEERR, 2 OK, 2 TSERR |
| Interface/literal method values | 46 TYPEERR, 32 OK, 4 TSERR |

## Step 2: RED per family (HEAD vs Node; Node exits 0 everywhere)

**Family 1, method values (`p01*`).** In `p01`, HEAD exits 0 and every line prints `TypeError Expected Box<number>, got Box<any>` (or `Box<string[]>`):
- A class's method, a generic class's method, an interface method on a class instance or on a literal, and an object literal's method, passed to a generic function type parameter.
- The same values held in a const, a field, an array, a map or a record, read through `?.`, or returned from a function.

The controls are OK: a static method value and a function declaration value.

Destructuring:
- From a class (`p01b`): exit 1, `PTS1001: destructuring the property make of a ClassMaker is not supported yet`. A non-generic method gives the same PTS1001 (`p01e`), so this is pre-existing and not about generics.
- From an interface (`p01c`) or a literal (`p01d`): TypeError.

**Family 2, `bind` / `call` / `apply` (`p02*`).**
- **TypeError (`p02`, exit 0):** `make.bind(undefined)`, `make.bind(null)`, a generic arrow's `.bind`, `c.make.bind(c)`, `c.make.bind(other)`, `m.make.bind(m)`, `lit.make.bind(lit)` and `C.make.bind(undefined)`.
- **PTS1001 (exit 1):** `lit.make.bind(other)` (`p02b`) and `m.make.bind(z)` through an interface (`p02c`), with "calling or binding a method of an object type with another object as `this`". The non-generic control (`p02d`) gives the same.
- **Not demands (`p02e`):** tsc infers T = `unknown` for `bind` with bound arguments, `.call` and `.apply`. As tsc types them they print what Node prints (`6 true`). With an added `as Box<number>` they throw TypeError (a checked downcast). An instantiation expression, `(make<number>).call(...)`, is OK.
- **Silent wrong `this` (`p02f`):** the generic method lines print `a:4`, `a:5`, `a:6`, `a:7` where Node prints `b:4` … `b:7`. The non-generic controls are right.

**Family 3, non-generic contexts (`p03`, `p04`).**

Already OK:
- A declaration in `.map`, to a user higher-order function, or to a non-generic const.
- A module-scope generic const in `.map` or to a higher-order function (D-102).
- A class method value in `.map` or to a higher-order function.
- An arrow for a function-typed property, and a method shorthand.

TypeError:
- An inline arrow in `map`, `flatMap`, `Array.from` or a user higher-order function. `map` gives `Expected Box[], got Array`.
- An aliased declaration in `.map`.
- A function-local const, a `let` or a parameter, given where the non-generic type goes.
- `const t: (x: number) => Box<number> = local`, a return, an object property, an array element, `as`, `| undefined`, and a class arrow field.
- An arrow property, a declaration or a local const where an interface declares a generic method (`p04`).

A class arrow field used as an interface method (`p04b`) is PTS1001: "using a `Impl` as a `{make(value): value}`…". This is pre-existing.

**Family 4, C196 (`p05`).**
- TypeError: a union-of-literals parameter key, a parameter of one literal type, a `let` key, `keys[1]`, and union keys through an interface or a literal.
- OK: `t[s as "make"]` with `const s: string = "make"`, and a `keyof` const. Both are const-initialised keys, so they are already constant keys.
- A key from `any` naming another method calls the right method (`wrap` ran) and then throws.
- In matrix isolation, the class cases fail with `TypeError Expected (value) => value, got undefined`. Only `"make"` joins the method tables there, so `wrap` isn't found. That is an honest error; nothing silently calls the wrong method.
- `C[key]` on a static method is PTS1001: "the class `C` as a value is not supported yet".

**Family 5, C205 (`p06*`).**
- TypeError: `im.make(yield 1)`, `im[K](yield 1)` and `lit.make(yield 1)`.
- OK: the class's own method, a dispatched method, a static method, a generic arrow const, a declaration, `c["make"](yield)`, and all `await` forms.

**Guards (`p07`).** The `any` boundaries all still throw:
- Lies through method values, `bind`, `.map`, a non-generic const, an arrow property, and an `any` read as `Box<number>`.
- Calls through `any`.

`p07b` shows the `any`-argument rule differs by demand kind:
- A value call converts an `any` argument with a check and finds the instance (D-148): OK.
- A call by name keeps the erased copy (D-191): TypeError.
- A method value keeps it too: TypeError.

## Step 3: root causes
- **Family 1:**
  - **Class and generic class methods:** `generics.go:genericMethodValue` → `valueInstance` → `erasedInstance` → `methods.go:boundMethod`. The bound wrapper has no `.instances` and calls `pts_fn_C_make_dynamic` (`out/c01.c` lines 112–121 and 211–214; the declaration's closure, lines 95–107, does list its instance). Only static methods are recorded as erased values (`generics.go:586`), and `specialize` pairs a value demand only with erased values, so no `C_make_number` is ever made.
  - **Interface and literal methods:** `methods.go:methodValue` → `objectTypeMethod` → a `boundMethod` over `InterfaceCall` or a field `CallValue`. The literal's field closure does list instances by name (D-191), but the wrapper never looks them up.
  - **Abstract methods:** reading one as a value makes an instance of the bodyless declaration, and `instanceBody` (`generics.go:950`) dereferences its nil body.
  - **What would fix it:** the wrapper could list instances that run on its captured receiver: the class's instance, a dispatched by-name call, or a by-name call on an interface or literal.
- **Family 2:**
  - **Function values:** `methods.go:functionMethodCall` makes a new closure whose body is an erased `CallValue` of the wrapped value; it ignores the instances that value lists.
  - **Class generic methods:** `isInstanceMethod` and `methodOf` only look at `membersAt().methods`, and generic methods aren't in it. They take the function path, bound to the original object, and `thisArg` is evaluated and dropped. That is the wrong-`this` bug.
  - **What would fix it:** with no bound arguments, the wrapper's demand signature equals the wrapped value's, so it could forward the instance lookup.
- **Family 3:**
  - **Adapters:** `closures.go:adaptSkipping`, reached from `lower.go:convert` and from `objects.go:propertyValue` → `functionAsMethod`, calls `adapted->code` erased (`out/c02.c:293–305`). Yet `local` lists `local_number` (`c02.c:115–127`). The context's signature gives the type arguments (`PtsInstantiateSignatureInContextOf`); only declarations (`valueInstance`) and D-102 consts (`lower.go:binding`) use them today.
  - **Array callbacks:** `callbacks.go:callbackMethodCall` keeps the erased callback, so `map` builds a `value[]` that is then checked into `Box[]`.
  - **Interface method fields:** `functionAsMethod`'s contextual signature is generic, so it needs instances forwarded by name.
- **Family 4:** `lower.go:keyedElementRead` → `DynamicGet` → a run-time-bound method → `valueCall` → `pts_instance_find`, which finds nothing (`out/c03_key.c:227`). Two possible fixes:
  - a by-name call whose name is the key's run-time value;
  - a switch over the key's literal types to the dotted calls, with today's path as the default for keys from `any`.
- **Family 5 (verified):**
  - `generators.go:yieldPoint` returns `ir.Resumed{}` unconverted, while `promises.go:awaitPoint` converts with a check.
  - `instances.go:namedInstanceCall` → `passedAsIs` rejects dynamic values (D-191).
  - Evidence (`p06b`): `(yield 1) as number` works and so does `const r = yield 1; im.make(r)`, but `im.make(0 + (yield 1))` still throws.

## Step 4: the H09b3b matrix (575 cases, about 5 minutes at 2 jobs)
Generated by `tools/gen.py`; re-run with `tools/run_matrix.sh PTS HOME OUT.json [NODE]`, or `run_matrix.py --pts --home --node`. On HEAD: 136 OK / 378 TYPEERR / 48 ICE / 13 DIAG / 0 CRASH.

| Family | Cases | HEAD result |
|---|---|---|
| Values (value form × kind × consumer × shape/type) | 486 | 116 OK, 316 TYPEERR, 46 ICE (all `disp`), 8 DIAG (class or static destructuring) |
| Guards (the liar kind) | 8 | 8 TYPEERR, as they must |
| Runtime keys (C196) | 35 | 30 TYPEERR, 5 DIAG (static `C[key]`) |
| Suspensions (C205) | 26 | 20 OK, 6 TYPEERR (yield through iface, ifacelit, lit) |
| `.call` / `.apply` | 20 | 18 TYPEERR (`as`), 2 ICE |

The form × kind tables are in `matrix/report-head.md`.

## Step 5: proposed tests (Node passes every expect block; details in `tests/verdicts.txt`)

| Test | HEAD result |
|---|---|
| `generic_method_values_passed_on` | 12 of 14 lines TypeError |
| `generic_bound_values` | 9 of 10 lines TypeError |
| `generic_method_call_bind_this` | 4 lines print `a:N` instead of `b:N` (silent) |
| `generic_dispatched_method_values` | exit 3, PTS9001 |
| `generic_values_nongeneric_contexts` | 17 of 20 lines TypeError |
| `generic_element_access_runtime_keys` | 10 of 15 lines TypeError |
| `generic_method_calls_yield_arguments` | 3 of 7 lines TypeError |
| `generic_value_forms_any_boundaries` (no-diff guard) | passes; differs from Node by design |

## Step 6: prototypes (`proto/*.diff`)
- **`c205-yield-arguments.diff`:** in `passedAsIs`, a yield's resumed value of a checkable, non-`any` tsc type is converted with a check, as the class's direct call does.
  - Matrix: 6 TYPEERR → OK, nothing else moved.
  - The yield test passes and the guard passes.
  - With `next()` and no value, the interface call now throws `Expected number, got undefined`, the same as the class path.
- **`c169-adapter-demand.diff`:** the adapter (and array callbacks) call the instance of the context's signature, with the erased copy as the fallback.
  - Matrix: 55 TYPEERR → OK, nothing OK → not OK.
  - Lines 6–7 of the skipped `generic_method_factories` test now pass.
  - Still failing: `flatMap`, `Array.from`, interface method fields, an inline arrow in a `const` declaration, and `map` with a promise result.
  - In the guard, the `.map` lie message changes to `Expected Box<number>, got Box<string>`; it still throws.
- **Both:** `TestRun` (681 pass, 98 skip) and `TestCompilerFailsCleanly` pass, exit 0. Not run: `TestRunMatchesNode`, the corpus, the Go unit tests, and a HEAD baseline suite of my own.

## Open questions
1. If method values list instances, value calls would pass `any` arguments to them with a check (D-148), while the same method called by name keeps the erased copy (D-191). Which rule should apply?
2. Should `.call` / `.apply` with an `as` cast stay TypeError? tsc types them `Box<unknown>`, and the cast is a checked downcast.
3. Do the dispatched-value ICE and the wrong-`this` bug belong in H09b3b or in H29? I only checked that they are pre-existing on HEAD; I did not bisect.
4. Should a C205 fix also cover `0 + (yield 1)`? The prototype handles a bare `yield` only.
