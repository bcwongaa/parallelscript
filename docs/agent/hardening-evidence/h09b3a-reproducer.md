# H09b3a reproducer report (reproducer, claude-opus-5-5, xhigh)

Saved by the parent from the child's final message, unedited below the rule. Probes: [h09b3a-reproducer-probes.tar.gz](h09b3a-reproducer-probes.tar.gz) (sha256 `33b1039d…`; `INDEX.txt` inside; unpacked under `/tmp/h09b3/repro/` in the session). The reproducer worked in its own worktree of `4ae2915e9` (`.claude/worktrees/agent-ae1b0dd3e886fa541`), left clean, nothing committed.

---

# H09b3a reproducer report: C179, C185, C188, C170

All four families reproduce on HEAD `4ae2915e9`. Each one has a RED test that fails on HEAD and matches Node, and the root cause is pinned to a file and line. A prototype (uncommitted, saved as a diff) fixes all of them. In the generated matrix it moves 288 TYPEERR to OK with nothing worse, and the whole `pts/tests` package passes with it.

Along the way the driver's fallback hid a bug in an early prototype: the program still ran, but quietly lost all its by-name instances. Every prototype run since then used a debug print to show fallbacks, and the only fallbacks left are in the programs that are PTS1001 on HEAD too.

My worktree is clean: nothing is committed, and every edit was saved as a diff and then reverted. The archive is `/tmp/h09b3/repro/probes.tar.gz` (its `INDEX.txt` describes each file), and `/tmp/h09b3/repro/DONE` is touched.

## Setup and commands
- Submodules were initialised with `git submodule update --init --reference …` for each library. `GOMAXPROCS=2 make build` exited 0, and that CLI was copied to `/tmp/h09b3/repro/pts-head`.
- Node runs on an `.mts` copy: `TZ=America/New_York node --no-warnings --experimental-transform-types`. `PTS_HOME` is always the HEAD worktree, because the prototype doesn't touch the runtime.
- Probe runner: `tools/tri.sh FILE.pts…` prints the pts output and exit code, then Node's. `PTS=<cli>` picks the CLI and `NO_NODE=1` skips Node.
- Matrix: `python3 -I tools/gen.py matrix/cases2` writes 496 cases. `tools/run_both.sh matrix/cases2 m2` runs HEAD, then the prototype, with 2 jobs each (about 9 minutes each). `python3 -I tools/report.py matrix/m2-proto.json matrix/m2-head.json` prints the table and the moves.
- Test verdicts: `python3 -I tools/verdicts.py tests/verdicts.md tests/*.pts`.

## 1. RED evidence (HEAD exit 0 unless stated; full outputs in `head_all.txt`)

| Repro | HEAD | Node |
|---|---|---|
| **C179** `fv?.(5)`, `holder.f?.(5)`, `holderMaybe?.f(5)`, `dispatched?.make(5)`, `iface?.make(5)`, `ifaceLiteral?.make(5)`, `own?.make(5)` (literal's own type), `union?.make(5)` (a01) | `TypeError Expected Box \| undefined, got Box<any>` | `6` |
| C179 `some?.many(5)` (`Box<T>[]` result) | `TypeError Expected Box[] \| undefined, got Array` | `10` |
| C179 still works: nullish receiver (`undefined`), `sure?.make(5)` on a non-nullable receiver, a declared `Box<T> \| undefined` result, a `Promise<Box<T>>` result, a direct class method `plain?.make`, the declaration `make?.(5)` | same as Node | — |
| **C185** `m["make"](5)`, `lm["make"]`, `literal["make"]`, `quoted["make"](6)`, `new ClassMaker()["make"](8)`, `d["make"]` (dispatched), `u["make"]` (union), `m[K]`, `m[k]` with `k: "make"`, `mm?.["make"]` (b01) | `TypeError Expected Box<number>, got Box<any>` | `6` / `7` / `9` |
| C185 `h["f"](5)` (a function-valued property), `quoted.make(6)` | OK | — |
| C185 `this["make"](3)`, and non-constant keys `t[key]` with `key: "make"\|"wrap"` and `t[keys[1]]` (b02) | TypeError | `4` / `6` |
| C185 neighbours `Parent["smake"](5)`, `super["make"](x)`, an overloaded method via `["make"]` (h01) | PTS1001, exit 1 | all print |
| **C188** `Ops.orElse<number>({tag:"some",value:3},0)` and the inferred form, through the literal's own type, an interface of a class, an interface of a literal, and a union (c01, c02); optional argument given, spread, getter, the `none` literal, a class instance for the union (c03) | `TypeError Expected Box<number>, got Box<any>` | `4` |
| C188 already OK on HEAD: `{ value: T }`, interface, nested and array-of-literal parameters, `Opt<T>[]` and `{opt: Opt<T>}`, and value and dispatched calls given union literals | OK | — |
| **C170** value call, rest after another parameter, zero rest arguments, `?.`, interface, interface of a literal, literal's own type, dispatched, string elements (d02) | `TypeError Expected Box<number[]>, got Box<any[]>` | `3` / `0` / `4` |
| C170 direct calls: the declaration, a class method, a D-102 const arrow, a tuple rest, a spread | OK | — |
| C170 `fm(...nums)`, spreading into a generic value's rest (d04) | PTS1001 `spreading a number[] into a rest parameter of type value[]`, exit 1 | `3` / `4` |
| C170 neighbour, `generics.go:515` (d07): `return errs({kind:"empty"})` where a `Result<number, ParseError>` goes | PTS1001 `converting {error: …} to … is not supported yet`, exit 1; the same call without a rest parameter compiles | `empty` |
| Earlier probes still RED: x05, i02 r3, p13, q12, i07 | as recorded in the ledger | — |

## 2. Where each call falls off the demand path (HEAD line numbers)

**C179.** tsc builds an optional-call clone of every candidate signature (`checker.go:8933/8976`, `getOptionalCallSignature`). The resolved generic signature is an *instantiation* of that clone, so the call-chain flag sits on `resolved.Target()`, not on `resolved` (`types.go:1267`). `getReturnTypeOfSignature` then adds `undefined` (`checker.go:19931-19932`).

In the lowering, `demandAt` (`instances.go:94-110`) computes `want.Result` as the union `Box<number> | undefined`. `erasedTarget` returns the same union, because `optionalChain` marks the call as `chainRests` (`chains.go:53`, read at `generics.go:259`). So the demand is made, but `fitsInstance`/`isCovariantResult` (`instances.go:947-956`) rejects every instance returning `Box<number>`. The run time finds no entry, takes the erased copy, and `fromErased` (`:334`, `:352`, `:384`) throws.

Only results that already hold `undefined`, or dynamic results, escape this.

**C185.** Every method path requires `KindPropertyAccessExpression`: `memberDeclaration` (`lower.go:944-946`), `genericMethodAt` (`generics.go:661-662`), `isUnionReceiver` (`union_objects.go:17`), and the name reads at `objects.go:1072` and `union_objects.go:141`. So `call` falls through to `valueCall` (`lower.go:1804` → `closures.go:448`).

`m["make"]` is then read as a keyed element (`lower.go:2370-2402`): a `DynamicGet` that finds a bound wrapper of the erased copy. `instanceValueCall` (`closures.go:468`) makes a value demand (`name ""`), and `reaches` (`instances.go:676`) pairs that only with erased values. The wrapper lists no instances. Even a direct instance call of a class's own generic method is lost this way.

**C188.** `namedInstanceCall` lowers the arguments once, to the erased copy's parameter types (`instances.go:379`). `Opt<T>` erases to a dynamic value, so the literal is boxed as its own shape. `passedAsIs` (`:461`) unboxes it and asks `passesAsIs(shape, Opt<number>)`, which for a union target only checks `u.Has(from)` (`:590`).

The union's members are interfaces, so a shape or class that *provides* one member is not a member. The result is no demand. A `{ value: T }` parameter works because `passesAsIs`'s interface case accepts a provider. Value calls and dispatched calls work because `instanceCall` (`:284`) lowers the arguments to the instance's types.

**C170.** `demandAt` rejects `HasRestParameter()` (`instances.go:96`). Even without that line, the rest would still fall off at three more points:
- the per-parameter check (`:104`) compares `...value[]` with `...number[]`;
- `holdsUnverified` has no `*ir.Rest` case and returns true (`:570`);
- `passedAsIs` has no rule for the rest's new array.

Two neighbours sit nearby:
- `resultTypeArguments` (`generics.go:515`) skips rest signatures, because its argument check compares each argument with the whole `T[]`. That is d07's PTS1001.
- `restArray` (`lower.go:2066-2072`) rejects spreading a typed array into a `value[]` rest anywhere, generic or not (d06).

## 3. Proposed tests (`tests/`; verdicts in `tests/verdicts.md`)

| Test | Node | HEAD | clean | proto | proto without chain / elem / lit / rest |
|---|---|---|---|---|---|
| generic_optional_chain_calls (C179) | PASS | FAIL 12/18 | PASS | PASS | FAIL only without chain |
| generic_element_access_calls (C185) | PASS | FAIL 11/14 | PASS | PASS | FAIL without elem; 1 line without chain |
| generic_union_literal_arguments (C188) | PASS | FAIL 11/17 | PASS | PASS | FAIL only without lit |
| the skipped generic_method_literal_arguments (C188) | PASS | FAIL 2/2 | PASS | PASS | FAIL only without lit |
| generic_rest_parameter_calls (C170) | PASS | FAIL 11/13 | PASS | PASS | FAIL without rest; 1 line without chain |
| generic_rest_spread_arguments (C170) | PASS | PTS1001 | PASS | PASS | PTS1001 only without rest |
| generic_rest_result_contexts (`generics.go:515`) | PASS | PTS1001 | PASS | PTS1001 | (only the clean diff has this fix) |
| generic_call_forms_any_boundaries (`// no-diff:` guard) | differs, by design | FAIL 3/10 (message text only) | PASS | PASS | — |

The guard lines that must keep working are inside the RED tests:
- nullish receivers give `undefined` without evaluating the arguments;
- the receiver and each argument are evaluated once, in order;
- `?.` on a non-nullable receiver;
- a declared `Box<T> | undefined` result;
- calls through `any`;
- a function-valued property and a non-generic method read by element access;
- a variable of the union type, value and dispatched calls, arrays and nested literals;
- direct calls, and `any` rest arguments.

The no-diff test pins D-155's liars through every new call form, calls through `any`, and D-191's "no dynamic value reaches an instance" (an `any` argument through `?.`, an `any` among rest arguments). On HEAD its three `?.` lines are TypeErrors too; only the message changes, from `Box | undefined` to `Box<number>`.

## 4. Matrix (`matrix/cases2`, 496 cases)

The dimensions are 12 call forms × 8 demand kinds × 4 result shapes (`Box<T>`, `Box<T>[]`, `Promise<Box<T>>`, `Box<T> | Two<T>`) × 4 type arguments (number, string, class, array).

- **HEAD:** 208 OK, 288 TYPEERR. No WRONG, CRASH, DIAG or ICE.
  - `opt` fails 6 of 7 per kind, all but the promise result; `elemopt` fails for every kind.
  - `elem` and `elemconst` fail for every kind except the value kind.
  - `lit` and `litcls` fail for the four by-name kinds only.
  - `rest`, `restopt` and `restmix` fail for every kind except direct calls.
- **Prototype and clean diff:** 496 OK. 288 TYPEERR→OK, no OK→not OK, no other output changed, and no fallback seen.

The first 424-case version moved from 193/231 to 424 OK.

## 5. Prototype

Two diffs, both checked to apply to HEAD:
- `proto/proto-clean.diff` is the proposed shape: 126 lines changed in 6 files plus the new `pts/lower/element_calls.go`.
- `proto/proto-switch.diff` is the version I measured. It adds per-family on/off switches and a debug print of each driver fallback.

**C179**
- Two new checker exports in `internal/checker/pts.go`, which would need listing in FORK.md. `PtsWithoutCallChain` re-instantiates the candidate without the chain flags, and `PtsIsOuterCallChain` detects the case.
- `demandAt` builds the demand from the signature without `?.`, and skips the `erasedTarget` comparison for chained calls, since the chain itself converts the result.
- `fromErasedFor` converts the erased copy's result to the instance's result type.
- Visible change: when no instance is found, the TypeError now reads `Expected Box<number>` instead of `Expected Box | undefined`.

**C185**
- `constantKeyCall` sends `o["m"](…)` and `o[K](…)` to the dotted paths: `genericMethodCall`, `genericCall`, `interfaceMethodCall` or `unionMethodCall`.
- It only does so for a generic method, a pure key (a literal, a name, or either under `as`/`satisfies`), a callee that isn't parenthesized, and not in the last fallback.
- Side effect: element-access statics, `super`, and overloaded methods, which are PTS1001 on HEAD, now work.

**C188**
- `passesAsIs` accepts a non-union object that provides one member of the union as it is. That is the member `toUnion` takes it as, so the instance gets the same object with no copy.
- It refuses when the object fails `!leavesOut` or holds dynamic values (`verifiesObjects`). D-192's `takesUnverifiedObjects` still runs first, so `Opt<Box<T>>` stays on the erased copy.

**C170**
- Rest demands are allowed, but not in DeclaredInstances, since the base never made them.
- `isErasedRest` lets the instance's typed rest stand in for the erased copy's `...value[]`.
- The erased copy gets a checked copy of the call's own new array, which no one else can see.
- `holdsUnverified` gains a `Rest` case.
- `passedAsIs` rebuilds the boxed rest arguments into the instance's element type, converting each one as it is.
- This last step is where I found my own bug: rest elements of a union type were emitted raw, clang rejected the C, and the driver silently fell back to ValueInstances.

**`generics.go:515`.** Rest arguments are checked against the rest parameter's element type, or the whole array for a spread, instead of skipping rest signatures.

**Constraints checked**
- No copy, check or adapter is added for the instance: the only copy is the fresh rest array on the erased branch.
- No dynamic value is passed to an instance; the guard test pins this.
- D-192's restriction holds: c03's `Box<number>` value still throws.
- D-155 holds: the liars still throw.
- The fallbacks hold: rest demands and element-access routing are off in DeclaredInstances.
- Evaluation order holds: each value is bound once, in order, and only pure keys are skipped.

**Results with the prototype applied**
- The whole `pts/tests` package (with `PTS_REQUIRE_NODE=1` and `PTS_SMOKE_CLANG=1`, plus the new tests and an unskipped copy of the skipped test) gives 1,593 pass / 308 skip / 0 fail.
- `go test ./pts/lower ./pts/driver ./pts/cgen ./internal/checker/... ./cmd/pts` passes.
- The clean diff's focused run gives 326 pass / 78 skip / 0 fail, and the filtered smoke run with clang is OK.
- Generated C grows 8–28% on the tests, from the instances now being used (`tests/csize.txt`).
- Nothing broke.

**Gaps that remain, and these are outside this slice**
- Non-constant keys (`t[key]` with `key: "make" | "wrap"`), keys with side effects, and a parenthesized `(m["make"])(5)` still throw.
- `o?.m(x).value` (an inner call chain) is PTS1001 for every method, generic or not.
- A generic value with a tuple rest (`<T extends unknown[]>(...xs: T)`) is PTS1001.
- Spreading a typed array into a `value[]` rest is PTS1001 generally (d06), so by-name calls with a spread make no demand.
- D-095 object-union contexts for value calls (g01) are PTS1001 on HEAD and the prototype alike.
- Calls inside an erased copy with an erased receiver keep the erased copy in all three forms (h06).
- An `any` argument for an object-type parameter keeps the erased copy (D-191, C186), the same as the dotted form.

## 6. H09b3b quick check (§5)

All of these still fail on HEAD and are unchanged by the prototype:
- The skipped `generic_method_factories.pts`: the first three lines pass; the last four fail. `method value` and `bound function` throw `TypeError Expected Box<number>, got Box<any>` (Node prints `10 4`). `inline arrow in a non-generic context` and `erased value in a non-generic context` throw `TypeError Expected Box[], got Array` (Node prints `3` and `4`).
- `const t: (x: number) => Box<number> = generic; t(5)` throws `TypeError Expected Box<number>, got Box<any>`; Node prints `6`.
- H09b1's `i04` `p2` (an arrow property where an interface declares a method) throws `TypeError Expected Box<number>, got Box<any>`; Node prints `6`. The rest of i04 matches Node.

Files are in `/tmp/h09b3/repro`:
- probes.tar.gz
- INDEX.txt
- proto/proto-clean.diff
- proto/proto-switch.diff
- tests/verdicts.md
- matrix/report-m2-head.md
- matrix/report-m2-proto.md
- compare_all.txt