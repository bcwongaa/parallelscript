# H09 reproducer report (opus-reproducer, claude-opus-5-5, xhigh)

Saved by the parent from the child's final message, unedited below the rule. Probes: [h09-reproducer-probes.tar.gz](h09-reproducer-probes.tar.gz) (`INDEX.txt` inside). Paths in the report are relative to the archive's `repro/` (unpacked under `/tmp/h09/repro/` in the session).

---

# H09 reproducer report: generic callable specialization

Summary: C09 and C70 both reproduce on the base commit. The two biggest failure families (generic arrows that aren't module-scope consts, and erased code holding a rest tuple) each have a small fix that I prototyped and ran against the full run, Node and corpus suites. The only test that changed was the one already pinning the known issue. Several smaller families are left (interface and object-literal methods, `bind`, non-generic contexts, promise results, rest parameters, dead narrowed branches). Five proposed tests are ready: four fail on base for the right reason and match Node, and one guards the checked-`any` boundary. I found seven defects outside H09 (section 9). Nothing was committed or pushed, and my worktree is back to clean.

## 1. Setup
- Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a1febe54d0bef208f`, HEAD `14ad0e6b73481c0eeb7e58fa3ad3606dca25e33d`. All five submodules were initialised from local references, and `GOMAXPROCS=2 make build` exited 0.
- CLI sha256: `609c4a4b45b111f4b1d7e87fbf7c1e2c56099f77374acd07c4d346f28b9b6367`. Node is v24.21.0, run on `.mts` copies.
- The archive is `/tmp/h09/repro/h09-reproducer-probes.tar.gz`; `INDEX.txt` inside lists how to run everything.
- The prototype compilers were built with `go build -overlay`, so the tree was never edited for them. The one runtime experiment was applied to my worktree's `convert.c`, tested with a separate `PTS_CACHE_DIR`, then reverted with `git checkout`. `git status` is clean.

## 2. RED (`bash /tmp/h09/repro/cmp.sh <W> …`)

| Probe | pts | Node |
|---|---|---|
| `red/c09_08_residual.pts` (GPT probe) | `TypeError Expected Box<number>, got Box<any>` | `box 9` |
| `red/c09_local_const.pts` (the parent's function-local const in `main`) | `TypeError Expected Box<number>, got Box<any>` | `local const 6` |
| `red/c70_tuple_rest_iface.pts` | `Uncaught TypeError: Expected number[], got Array`, exit 1 | `[ 1, 2 ]` |

**What causes C70:**
- It isn't specific to generics. Any tuple held as `any` fails to convert to an array type.
- `red/c70_siblings2.pts`: `any` holding a tuple, converted to `number[]`, `readonly number[]` or `Array<number>`, gives `Expected number[], got Array`. Converted to `any[]` it gives `Expected value[], got Array`. Only tuple→tuple works.
- In `pts/runtime/src/convert.c`, the array case of `convert()` needs `object->type->kind == PTS_KIND_ARRAY`, so tuples are rejected there. The tuple case does accept arrays, so the conversion only works in one direction.
- Erased code keeps the rest parameter `[T, T]` as a boxed tuple, so the typed call's conversion of the result hits this.
- The same method called through its class (monomorphized) is an honest PTS1001 instead: `using a [number, number] as a number[] where it may be changed or kept`. Non-generic code gives the same error.

**C70 siblings** (`c70/gen.py`, 432 cases; `c70/summary-base.txt`):
- `TypeError … got Array` in 63 cases: rest tuples of length 1, 2 and 3, returned as `T[]`, `readonly T[]` or `Array<T>`. It happens through an interface, an object-literal method, an object literal behind an interface, an abstract (dispatched) method, a generic function value, and a generic const called by name. That last one has no instance because a `T[]` result doesn't count as "makes generic instances".
- No failure when the body copies first (`[...items]`) or passes the items on (`collect(...items)`).
- `[T, ...T[]]`: PTS1001 `rest parameters of tuples with rest elements are not supported yet` (21 cases).
- Optional element through a function value: PTS1001 `using a (value, value) => value as a (value, value, given) => value, which may leave out an argument it needs` (6 cases).
- Two wrong outputs that are separate defects (N1 and N2 in section 9).
- The 108 tsc errors are my generator's mistakes: `[T, T?]` isn't assignable to `T[]`.

## 3. Inventory

The 754-case matrix is described in section 6. The table below covers the `Box<T>` result shape on base. "O" means it matches Node; "T" means `TypeError Expected Box<…>, got Box<any>` where Node prints the value.

| Definition form | Reaches an instance on base | Falls back to the erased copy and throws on base |
|---|---|---|
| module `function` declaration, nested in a function, nested in a method; static method; module `const` arrow or function expression; a const arrow calling a declaration | direct, explicit type args, param, var, field, generic call signature, through a generic decl, through a value, `map` context | `bind`; promise results through a value |
| `const mk = make` (local alias of a declaration) | the same | `bind`, `[x].map(mk)` (adapter) |
| generic instance method read as a value (`c.mk`) | direct call, `map` context | param, var, field, call signature, through: the bound erased method lists no instances |
| function-local `const`/`let` arrow or function expression; inline arrow or function expression; module `let`; contextually generic `const mk: <T>(x:T)=>Box<T> = (x)=>…`; returned by a factory (once or each call); class field; static field; object property; array element; Map value; IIFE; default parameter; in a generic function's instance or a generic class's method; curried; local const calling a declaration | nothing | **every use** (T) |
| capturing a parameter, a cell, `this`, a loop variable; recursive local | nothing | every use (T) |
| interface generic method of a class; object-literal generic method | nothing | every use (T); `bind` is PTS1001 `calling or binding a method of an object type with another object as this` |
| `namespace` and `var` | n/a | PTS1002 by design (`matrix/smoke.json`) |

Hand probes (base outputs are next to each file):
- `inv/i01_context_only.pts` (type argument from the result only): the declaration works; inline and local arrows give `Expected Box<number[]>, got Box<any[]>`.
- `inv/i02_loop_closures.pts`: `Expected [Box, number], got Array`. Identity is unchanged: `true false`.
- `inv/i03_curried_pair.pts`: `Expected Pair<string, number>, got Pair<string, any>` (module and nested-in-const) and `Pair<any, any>` (local).
- `inv/i04_generator_async.pts`: the module generator const works. Local async and generator arrows throw. Promise and Generator results lower to `ir.Value`, so `demandAt` returns nil (`want.Result == ir.Value`) and no demand is ever made.
- `inv/i05_explicit_multi.pts`: an explicit union type argument throws; two type parameters give `Expected [Box, Box], got Array`.
- `inv/i06_identity_props.pts`: matches Node (`true mk 1 [Function: mk]`).
- `inv/i07_rest_optional_params.pts`: rest parameters `<T>(...xs: T[]) => Box<T[]>` throw `Expected Box<number[]>, got Box<any[]>` because D-148 skips them (`HasRestParameter`). Optional and default parameters work.
- `inv/i08_call_apply.pts`: tsc types `.call`/`.apply` results as `Box<unknown>`. Reading `.value` works; `as Box<number>` throws.

## 4. `any` paths

| Path (probe) | Base | Should it change |
|---|---|---|
| declaration, const, static or module-const value → `any`/`unknown`/`any[]`/record → generic function type, then a typed call (a01) | **works** (the conversion keeps the closure, and the instance is found) | stays |
| function-local arrow → `any` → generic function type (a06) | TypeError | **fixable**: works with prototype 1 |
| erased copy → `any` → *non-generic* function type `(x:number)=>Box<number>` (a06). The same happens with no `any` at all: `const t: (x:number)=>Box<number> = localArrow` | TypeError | **fixable**: the adapter (D-075) wraps the closure and hides its instances |
| erased copy called through `any`, result used dynamically (a02) | works | stays |
| erased copy called through `any`, result assigned to `Box<number>` (a02, a03 literal through `any`) | TypeError (Box<any>) | **inherently dynamic**: there is no signature and no type arguments, so this is the documented SPEC §8 divergence (D-078, D-154) |
| class instance → `any` → `as C`, then the method is called (a03) | works (monomorphized) | stays |
| → `as Interface` (a03). The same fails with **no `any`**: `const m: Maker = new C(); m.mk(5)` | TypeError | **fixable**: this is D-148's interface known gap, not an `any` problem |
| → `as AbstractBase` (a03) | works (dispatched) | stays |
| object literal → interface, with or without `any` (a03) | TypeError | fixable but harder (section 8) |
| typed call inside erased code reached by a typed demand (a05 "through value") | works | stays |
| curried value / curried direct (a05) | `Expected [Box, Box], got Array` | fixable: works with prototype 2 |
| `.call`/`.apply` then `as Box<number>` (i08) | TypeError | inherently dynamic: tsc gives `Box<unknown>` |
| **lying functions (a04)**: returns `Box<string>`; generic decl returning `Box<string>`; returns a plain object; `(x:number)=>Box<number>` asked for a `Box<string>` | `TypeError Expected Box<number>, got Box<string>` / `got Object` / `Expected number, got string`. Node prints wrong-typed values | **must stay** (D-155). Unchanged under prototypes 1 and 2 (`anyp/proto.txt`) |

## 5. narrowsAway findings
- **Dead branches don't even compile today, directly by name.** `narrow/n03_direct_dead_branch.pts` (`show("s")` with `if (typeof x === "number") console.log(x + 1)`) fails with `8:29 - error PTS1001: converting string to number is not supported yet`.
- `n02` shows two more: `x.length` after `Array.isArray(x)` for a number is PTS1001 (the type is `number & any[]`, which isn't `never`), and `typeof x === "number" ? new Box(x) : new Box(x)` is PTS1001 `conditional expression with operands of types Box and Box`.
- `n04` (early return, `switch (typeof x)`) gives the same errors. Node runs every one.
- **Removing the guard** (`narrow/overlay`, `narrowsAway` returns false): `n01` and `run/generic_factories_fallbacks.pts` both fail with `12:29 - error PTS1001: converting string to number is not supported yet`. So the guard is what keeps those programs compiling.
- **Dead-branch prototype** (`narrow/dead/deadbranch.patch`, about 30 lines):
  - What it does: the guard is removed, and in instance code an `if` branch is lowered as an empty block when it reads a variable whose flow type is not `never` in the generic code but `never` once instantiated. It doesn't descend into nested branches, logical right operands, loops or functions.
  - Results: n01 and n03 match Node, and the fallbacks test's `show` string instance prints `string s`.
  - Still failing: n02 (the intersection case and the `?:` case) and n04's early return and `switch`.
  - Combined with prototype 1, the full run/Node/corpus suite (`proto/gotest-combined.log`) had only the fallbacks test fail, and its new output **equals Node's**.
- **Recommendation**: feasible, and best as its own slice. Lower dead regions as a trap (`pts_unreachable`-style abort with a message) rather than empty code. The regions to handle are if/else branches, `?:` branches, `&&`/`||`/`??` right operands, switch case bodies, loop bodies, and "rest of block after a top-level statement with a dead reference" (for early returns).
- The criterion must compare the generic flow type with the instantiated one. A plain "is `never`" check would also catch code tsc already considers unreachable.
- It is sound only because the instance's representation fixes the runtime type. Leave intersections like `number & any[]` out: branded `number & {…}` types are real values.

## 6. Generated matrix
- **Generator**: `python3 -I /tmp/h09/repro/matrix/gen.py OUT`, giving 754 programs: 37 definition forms × 12 use sites for `Box<T>`, plus 8 more shapes (`Box<T>[]`, `[Box<T>, T]`, `Box<T>|undefined`, `Box<Box<T>>`, `Map<string,T>`, `Map<string,Box<T>>`, `{value:T}`, `Promise<Box<T>>`) on a subset of forms and uses. Each program tries number, string, a class and an array, each in its own try/catch.
- **Runner**: `PTS_HOME=<W> [PTS_CLI=…] python3 -I matrix/run.py CASES RESULTS.json 2`, then `matrix/summary.py`. Runs take about 4–5 minutes with 2 jobs.

| Build | OK | TYPEERR | DIAG | TSERR | WRONG/ICE/CRASH |
|---|---|---|---|---|---|
| base | 214 | 464 | 2 | 74 | 0 |
| prototype 1 (non-capturing closures) | 470 | 208 | 2 | 74 | 0 |
| prototype 2 (+ captures) | 518 | 160 | 2 | 74 | 0 |

- The 74 tsc errors are `.call`/`.apply`, which tsc types as `Box<unknown>`; these are generator artefacts. The 2 DIAG are the `bind` PTS1001s from section 3.
- Neither prototype regressed any case.
- What still fails under prototype 2, by family: interface method 32, object-literal method 32, `bind` 29, promise result 26, non-generic `map` context 23, instance method value 6.
- The `{ value: T }` shape works everywhere on base, because a shape converts by checked copy.
- Per-shape tables are in `matrix/table-*.txt`.

## 7. Proposed tests (`/tmp/h09/repro/tests/`, checked with `python3 -I tests/check.py --pts … --home …`)

| Test | What it pins | Base | Node |
|---|---|---|---|
| `generic_arrow_factories.pts` (C09) | the probe; inline arrow and function expression; local const/let; contextually generic; type argument from the result only; two type parameters; factory; class/static field; property; element; Map value; in a generic function and a generic class's method | FAIL (18 TypeError lines) | MATCH; passes with prototypes 1 and 2 |
| `generic_arrow_captures.pts` | captures a parameter, a cell (`called by name 2 3`), `this` (`made 2`), loop variable, curried (module and local), nested in a generic const, recursive local; identity/name/length | FAIL (base prints `made 1`) | MATCH; passes with prototype 2 only |
| `generic_method_factories.pts` | interface call of a class's method; object-literal method (own type, and through an interface); method value; `make.bind(undefined)`; inline arrow and erased alias in `[1,2].map(…)` (`Expected Box[], got Array`); `Promise<Box<T>>` value | FAIL (all 8) | MATCH; still fails with prototype 2 |
| `generic_tuple_rest_results.pts` (C70) | interface, literal through an interface, literal `readonly`, abstract `Array<T>`, generic value, generic const; lengths 1–3 | FAIL (`Expected number[], got Array` ×6) | MATCH; passes with the runtime fix |
| `generic_values_any_boundaries.pts` (`no-diff` guard) | values through `any` that work, plus the a04 lies that must throw | PASS (the intended output) | differs, as intended |

- **Existing test to change**: `run/generic_factories_fallbacks.pts`.
  - With closures fixed, its lines 4–5 become `number 2` and `string s`.
  - With the dead-branch work as well, the whole output equals Node (`tests/generic_factories_fallbacks.node-output.txt`: `a number 3/number 2/string s/number 2/string s/a number 6/5`), and its `no-diff` line can go.
- No other run test changed under any prototype.

## 8. Causes and fix sketch

1. **Generic closures list no instances.**
   - Cause: `functionExpression` only calls `addErasedValue` for `genericConstNodes`, which `declareGenericConsts` fills for module-scope consts only.
   - Prototype 1 (`proto/proto1-closures.patch`): for any arrow or function expression that is generic (its own type parameters or its context's), captures nothing, isn't named, isn't async or a generator, and isn't made inside erased code, make a `genericFunc{node, module: Module.Init, outer: <subst it was made in>}` and call `addErasedValue`.
   - Result: C09 is fixed, and so are 256 matrix cases.
   - Suite (`TestRun`, `TestRunMatchesNode`, `TestCorpusMatchesNode`, 2 jobs): only the expected fallbacks-test change. I confirmed with `-v` that the Node comparisons actually ran.
   - Code size (`proto/csize-*.txt`, 829 programs): 3 changed, total +0.054%. `transducers` grew 8.9%, `church_combinators` 1.9%.

2. **Capturing closures.** An instance must run with the erased closure's environment, because the runtime value is still the erased closure.
   - Prototype 2 (`proto2/proto2-captures.patch`, about 130 lines):
     - new field `ir.Func.Env`;
     - the instance is created with `Closure=true`, `Outer`/`Captures` copied from the erased closure, and `arrows[]` set;
     - `instanceBody` rebinds the locals of captured symbols that were snapshotted when the closure was lowered;
     - `specializeValue` accepts `fn.Env == v.fn`;
     - cgen's `capturePrologue` reads `pts_closure_<Env>`;
     - `instanceTable` passes `pts_env` to closure instances.
   - Result: 304 matrix cases fixed, and the stress probes match Node (`proto2/p2_stress.pts`: two instances of an outer generic function, a nested closure, a cell shared with the erased copy, a variable declared after the arrow, constructor captures).
   - Suite: same as above, only the fallbacks change.
   - **Risks found:**
     - (a) Without the rebinding, the instance resolves a captured name to another instance's local, giving PTS1001 `using tag from outside the function` (`proto2b`).
     - (b) **Still broken in prototype 2**: an arrow that calls a nested function declaration, inside two instances of a generic function, fails with PTS1001 `using helper from outside the function` (`proto2/p2_stress2.pts`). `l.funcs` and `Self` are re-declared per instance. On base this program compiles and throws a TypeError at run time, so this would be a compile regression unless handled.
     - (c) In general, speculative instance bodies can fail to lower and turn compiling programs into errors. The fix should lower speculative instances in a trial mode (if the body reports, keep its errors back and don't list it), or prove captures are a subset of the erased closure's.
     - (d) Instance captures must be a subset of the erased closure's, otherwise cgen panics at `captureField`. Check after `instanceBody`.
   - `markCells` works unchanged, because the `Local` objects are shared. Identity, `console.log`, `.name` and `.length` stay the erased closure's (i06 matched).
   - Async/generator closures need item 4 first.
   - `maxInstances` is per closure. Every closure is paired with every demand of the same erased type, so code grows with closures × signatures.
   - The speculative limit error (N5) should become "don't list" rather than PTS1001.

3. **Non-generic contexts and adapters** (`[1,2].map(arrow)`, `const t: (x:number)=>Box<number> = local`, `any` → non-generic type).
   - Lower an inline generic arrow whose context is a non-generic function type as its instance directly (the D-078 rule declarations already follow), guarded by `narrowsAway` and `makesGenericInstances`.
   - In `adaptSkipping`, when `from.Result == ir.Value` and the source is a generic value, make the adapter body an `InstanceCall` on the wrapped closure, using the contextual signature as the demand.

4. **Promise/Generator results**: `demandAt` rejects `want.Result == ir.Value`.
   - Allow it when the checker result holds generic objects.
   - Risk: the signature key is built from IR types, so `Promise<Box<number>>` and `Promise<Box<string>>` with the same parameters collide. A collision fails with a checked TypeError at the `await`, so it is never silently wrong. The proper fix is a richer key.

5. **Rest parameters**: lift D-148's `HasRestParameter` exclusion. The instance's parameter is the packed typed array.

6. **Interface and object-literal generic methods** (`interfaceMethodCall` in `objects.go`).
   - Interface calls: add a by-name demand like `dispatchedInstanceCall`, paired with every class generic method of that name, via `pts_instance_find(obj, name, sig)`.
   - Shape fields and object literals behind interfaces: the method is an erased closure taking `this`. Look up the field closure's own instance table, either with a two-step runtime lookup or by giving the shape-method call a demand on the field closure.
   - Generic method values (bound) and `bind`: the bound closure needs entries that forward `pts_env`'s receiver or target, or `pts_instance_find` could see through a bound function that binds only `this`.

7. **C70.** Runtime experiment (`c70/runtime-tuple-to-array.patch`, about 20 lines): `convert()`'s array case also accepts `PTS_KIND_TUPLE` when copying. It copies `pts_tuple_length` elements, each converted, into a new array of the check's type. That is a checked copy, consistent with D-071 and D-155.
   - Results: all 63 TypeError cases fixed, `c70_siblings2` fixed, `generic_tuple_rest_results.pts` passes, no regressions in the C70 matrix.
   - Suite with that runtime: `ok` (`c70/gotest-runtime-fix.log`).
   - Alternative: make erased copies' tuple rest parameters `any[]`. That would also fix `items.slice()` inside erased code, but not N2 in typed code.
   - The typed path keeps its honest PTS1001.

## 9. New defects outside H09 (minimal repros in the archive)
- **N1** `c70/nongeneric_siblings.pts`: `function opt(...items: [number, number?]) { return items; } opt(1)` prints `[ 1, undefined ] 2`; Node prints `[ 1 ] 1`. This is in D-100's area.
- **N2** Same file: array methods of a tuple through `any` (`a.slice/map/indexOf/join/concat/includes/at` on `any` holding `[1,2]`) throw `TypeError a.slice is not a function`. Erased code hits it too, as `items.slice is not a function` (21 C70 cases).
- **N3** Direct calls of generic functions whose instance has a dead narrowed branch are PTS1001 (`narrow/n02`–`n04`; section 5).
- **N4** `inv/i09_many_demands.pts` (70 demands, so `Box` has more than 64 instances): after the PTS1001 there is a `PTS9001 internal compiler error … nil pointer dereference` and exit code 3. The stack runs `fromErased` → `dynamicConversion` → `prepareDynamicTarget` → `isCheckable`: a typed-nil `ir.Type` gets past the `target == nil` check.
- **N5** `inv/i10_many_function_instances.pts`: 70 legitimate demands give 6× `PTS1001 a generic function with more than 64 instances (mapOf)` with no polymorphic recursion. Node prints 2415. Speculative instances count against the limit.
- **N6** Message quality: `Expected [Box, Box], got Array`, `Expected Box[], got Array`, `Expected [Box, number], got Array` give no type arguments inside tuples or arrays (D-147 gives them only for classes).
- **N7** `im.mk.bind(undefined)` and `o.mk.bind(undefined)` on an object type's method are PTS1001 (`calling or binding a method of an object type with another object as this`).

## 10. Cost
I measured instructions per iteration with callgrind, taking the run with N=2e6 minus the run with N=1e6, using `PTS_THREADS=1` (`cost/run.sh`, `cost/results.txt`):

| Case | Instructions per call |
|---|---|
| direct `make(i)` (monomorphized) | 130.3 |
| through `f: <T>(x:T)=>Box<T>`, instance found, table of 1 | 183.3 (+53: lookup plus entry indirection) |
| same, wanted entry 16th of 16 (I checked the table order in the C) | 258.3, so about **5.0 per extra table entry** (linear scan) |
| `{value:T}` shape through an erased copy (boxing plus checked copy) | 687.4 |
| `{value:T}` shape through an instance | 189.3, about 3.6× cheaper than erased |

The scan is cheap at today's table sizes. If closures get instances for every demand of their erased type, tables grow, and a small hash or per-site cache keyed by signature would bound the cost.