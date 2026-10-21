# H09b4b2 review reports

The `reviewer` subagent's final messages (claude-opus-5-5, effort xhigh; read-only), saved verbatim by the parent; HTML entities the messages carried are written as characters.

## Round 1 (fix commit `441b2d300`, snapshot `snap1`)

Archive: [h09b4b2-review1-probes.tar.gz](h09b4b2-review1-probes.tar.gz) (sha256 `7313a3d8f72d9f71…`, 4,718 entries: probes in `p/`, outputs in `out/<name>/{node,fix,base}.txt`, `scripts/`, `emitc/diff-summary.txt`, `cg/counts.txt`).

---

# H09b4b2 review round 1: commit `441b2d300` is not ready

The commit has one high-severity regression: correct programs, with no cast and no lie, that ran right on the base now throw the trap's TypeError. There is also a compile regression from `returnsNeverValue`. I ran every repro below with Node (harness style), the fix CLI (`snap1`) and the base CLI.

The snapshot matches `git show 441b2d300:pts/lower/dead_code.go` byte for byte. The working tree has uncommitted parent edits to `dead_code.go`, `unions.go` and `checker/pts.go`. I only read those, never touched them, and reviewed the commit.

## Findings, by severity

### 1. HIGH: correct code with no lie now traps (verified)
`noValueOfItsIRTypePasses` checks `literalFree` on the read's own declared type only, and `literalFree` passes every class without looking at it. tsc's never can come from literal or enum types that the declared type doesn't show, and Node does reach that code. In every case below Node equals the base and the fix throws `TypeError: code that can't run ran: … a type predicate, assertion or cast was wrong`.

**(a) Class unions with literal discriminant fields** (`r34`, `r35`, `r33`, `r33b`). A subclass narrows a field of its base, and the base's own method writes it:
```ts
class Base { kind: string = "base"; rename(k: string): void { this.kind = k; } }
class Add extends Base { kind: "add" = "add"; n = 1; }
class Say extends Base { kind: "say" = "say"; text = "x"; }
function run(a: Add | Say): string {
  switch (a.kind) {
    case "add": return "add " + a.n;
    case "say": return "say " + a.text;
    default: { const other = a; return "other " + (other as Base).kind; }
  }
}
const a = new Add(); a.rename("del"); console.log(run(a));
```
- Node and base print `other del`; the fix traps.
- The same happens when a renamed Error subclass is written through `Error.name` (`r35`).
- It also happens with `Object.assign(new Add(), JSON.parse(...))`, a common way to load JSON into a class instance (`r33b`).

**(b) Destructured discriminated unions** (`r31`, no cast; `r32` covers a parameter pattern and a `for…of` pattern). `payload`'s own type has no literals, but its never comes from `kind`:
```ts
enum Kind { Add, Say }
type Payload = { n: number; text: string };
type Action = { kind: Kind.Add; payload: Payload } | { kind: Kind.Say; payload: Payload };
function run(action: Action): string {
  const { kind, payload } = action;
  if (kind === Kind.Add) return "add";
  if (kind === Kind.Say) return "say";
  const p = payload;
  return "unknown " + kind + " " + JSON.stringify(p);
}
const n: number = 7; const k: Kind = n;
console.log(run({ kind: k, payload: { n: 1, text: "hi" } }));
```
- Node and base print `unknown 7 {"n":1,"text":"hi"}`; the fix traps.
- The same `action` read without destructuring is not dead, so the rule is inconsistent with itself.
- `r30` is the variant through a literal union cast.

**(c) Equality with an enum-typed value** (`r36`):
```ts
function classify(code: number, expected: Kind): string {
  if (code === expected) {
    if (code === Kind.Add) return "add";
    if (code === Kind.Say) return "say";
    const c = code; return "other " + c;
  }
  return "mismatch";
}
// classify(7, k) where k: Kind = n (n = 7)
```
- Node and base print `other 7`; the fix traps.

From reading the uncommitted working-tree change only (not run): it re-runs flow analysis from `widestOfIRType(declared)`. In (a), (b) and (c) the declared type is already its own widest type, so I expect all three to still trap.

### 2. MEDIUM: `returnsNeverValue` breaks programs that compiled on the base (verified)
A never method or function whose body returns a value now has a dynamic result, while its counterpart's result is still void:
- **Override** (`r13`): a base `fail(x: never): never { throw … }` overridden by `fail(x: never): never { return x; }` gives `PTS1001: overriding Handler.fail with other parameter or result types`.
- **Interface** (`r14`): `class Lenient implements Reporter { unreachable(x: never): never { return x; } }` used as a `Reporter` gives `PTS1001: using a Lenient as a {unreachable(value): void}`.
- **Conditional** (`r15`): `debug ? strict : lenient` gives `PTS1001: conditional expression with operands of types (value) => void and (value) => value`.

Node and base both print the right output (`square circle` and `1 -1`). Plain calls, function values, arrays and object-literal implementations work (`r10`, `r11`, `r16`, `r17`).

### 3. LOW-MEDIUM: `isInferredParameter` treats a declared never as inferred (verified)
It returns true for any generic signature, so `function exhaustive<T>(x: never, fallback: T): T` loses the "destination declared never" exemption (`r18`). After a lying predicate:
- Node and base return `fallback`; the fix traps.
- Plain `assertNever` still behaves as in Node.

This contradicts D-197's own rule ("a destination declared never … not a never tsc inferred"). Methods of generic classes have the same problem. The check should look at the parameter's declared type, not at whether the signature is generic.

### 4. MEDIUM gap (not a regression): dead reads outside the trap units still don't lower (verified)
These plain-code positions still fail with PTS1001 on both fix and base, while Node runs the program (`r52`, `r53`):
- a `for` condition and a `for` update;
- a `do…while` condition;
- a `case` label expression;
- a read past `?.`.

None of these shapes is in the reproducer's 40-shape region family, and C220's acceptance asks for plain code tsc types never to lower.

### 5. INFO: a corpus program now passes
`pts/tests/corpus/functional/reactive_signals.pts` now compiles and its output equals Node's; the base gave `PTS1001: values of type Function`. The cause is probably `instantiate` mapping an uninhabited intersection to any (my guess; I didn't trace it). Its `// skip:` line is now stale, and the "same C but the fixed ones" claim should list it.

## What I checked and found right
- **Emit-c over all 1,112 programs** (run, errors, corpus, docs/examples), fix vs base: the C is identical apart from the 15 new or changed tests and `reactive_signals`. The base failed 12 of those that the fix compiles; for the other 4 only the C differs. There were no PTS9001s.
- **Compile cost** (callgrind instruction counts):
  - `sql_query_parser`: +0.16% over two pairs of runs; run-to-run noise is about ±2%.
  - `array_pattern_lazy`: +0.18%.
  - `any_iteration_lazy`: +0.09%.

  This is consistent with the claimed cost.
- **Stale intersection reads** now run where the base gave PTS1001, including writes, iteration and typed arguments (`r40`, `r41`, `r42`).
- **Predicate arguments** match Node in every form I tried: stale, methods, generics, overloads, spreads, `?.()`, asserts, tuples, keyed reads (`r20`).
- **Dead tails and a module-level dead statement** (`r50`, `r51`).
- **Possible unchecked routes**: replacer groups, `setTimeout` arguments, `next()` without an argument, deep casts, `Map.get()!`, records (`r04b`, `r05`, `r06`, `r60`–`r62`). All are checked at the boundary, and the base and the fix behave the same.
- **Structural class use** is PTS1001 on both (`r01`), so nominal disjointness holds.

## Pre-existing side findings (same on base and fix)
- A `this: void` parameter shifts the arguments: `PTS1001: converting string to undefined` (`r21`).
- `"a" in x` for a `declare`d field is true where Node gives false (`r38`).

## Test quality
- No guard test covers the no-lie routes in finding 1 or the dispatch cases in finding 2. `r31`, `r34`, `r36`, `r13`, `r14` and `r15` would make good guards.
- Style nits: the header comment in `dead_code.go` is badly re-wrapped (lines of uneven length), and the doc comment on `takesAnyValueAsIs` doesn't mention its new `goesAsItsDeclaredType` case.

## Verdict
Not ready. Finding 1 must be fixed: either stop shortcutting classes in `literalFree` for the never case, or don't trap when the narrowing went through literal or enum types (an equality with a literal- or enum-typed operand, a destructured sibling's discriminant). Finding 2 should be fixed, and finding 3 is small.

Everything is in `/tmp/h09b4b2/review1.tar.gz`: probes in `p/`, outputs in `out/<name>/{node,fix,base}.txt`, `scripts/`, `emitc/diff-summary.txt`, `cg/counts.txt`. It holds no binaries or C files, and I wrote the marker `/tmp/h09b4b2/review1.done`.

## Round 2 (round-1 fix `8023de8e1`, snapshot `snap2`)

Archive: [h09b4b2-review2-probes.tar.gz](h09b4b2-review2-probes.tar.gz) (sha256 `7c4a4eb8f276f8ab…`, 4,934 entries: probes in `p/r1/` and `p/r2/`, outputs in `out/<name>/{node,snap2,snap1,base}.txt`, `out/r1-summary.txt`, `emitc/diff-summary.txt`, `cg/counts.txt` and its generators).

---

# H09b4b2 review round 2: commit `8023de8e1` is not ready

Round 1's F2 (never functions), F3 (generic never parameter) and F5 (`reactive_signals`) are fixed, and F4 (dead reads outside a trap's first part) is fixed in plain code but not in generic instances. F1 (traps in correct code) is mostly fixed. Two problems remain:
- **New regression:** an enum value used as an array index in an exhaustive default no longer compiles; the base and the fix commit ran it.
- **F1's holes:** a discriminant read through a `const` alias still traps correct code that has no cast.

Everything below was run with Node (harness style), snap2, snap1 and the base.

## (1) Round-1 probes re-run on snap2
All 42 match Node or the base; none differs from both.
- r13–r15 and r18 now match Node.
- r30–r36 (the F1 cases) match Node and the base.
- r52 and r53 (F4 shapes) now compile and match Node.
- The rest match the base, as in round 1: pre-existing behaviour, PTS1001s, or the base's `undefined` for never functions (C239).

## (2)–(4) Findings, by severity

### 1. HIGH: compile regression — an enum's never read can't index an array (verified)
`readsAsItsDeclaredType` now boxes every read that "may hold an untested value", except where the declared type goes. An array index is not such a place, and a boxed value can't index:
```ts
enum Kind { Add, Say }
const names = ["add", "say", "x", "y", "z", "w", "v", "seven"];
function label(c: Kind): string {
  switch (c) {
    case Kind.Add: return "add";
    case Kind.Say: return "say";
    default: return names[c] ?? "?";
  }
}
const n: number = 7; const k: Kind = n;
console.log(label(Kind.Say), label(k));
```
- Node, base and snap1 print `say seven`.
- snap2 fails with `PTS1001: indexing an array with a value is not supported yet` (`s27`).
- Reads, writes, `++` and typed-array indexes all fail this way (`s22`, `s24`, `s25`).
- About 25 other uses of the boxed value match Node (`s21b`, `s23`).

This is the same kind of read the guard `narrowed_never_enum_literal_defaults` covers; it just lacks an index use.

Suggested fix: a read that isn't stale and isn't a loose local always holds a value of its declared IR type. An enum's other number is still a number, and a renamed `Add` is still an `Add`. Such reads can read as their declared type wherever that type lowers, and only need boxing where tsc's never-typed use doesn't fit the declared type.

### 2. HIGH: F1 hole — discriminant aliases still trap correct code with no cast (verified)
`relatedSymbols(a)` is just `{a}` when `a` is a parameter. But tsc also narrows `a` through `const { kind } = a` and through `const kind = a.kind` (`getCandidateDiscriminantPropertyAccess`):
```ts
class Base { kind: string = "base"; rename(k: string): void { this.kind = k; } }
class Add extends Base { kind: "add" = "add"; n = 1; }
class Say extends Base { kind: "say" = "say"; text = "x"; }
function run(a: Add | Say): string {
  const { kind } = a;               // or: const kind = a.kind;
  switch (kind) { case "add": return "add " + a.n; case "say": return "say " + a.text; }
  const other = a;
  return "other " + (other as Base).kind;
}
const a = new Add(); a.rename("del"); console.log(run(a));
```
- Node and base print `other del`.
- snap2 (and snap1) throw `TypeError: code that can't run ran: \`a\` narrows to \`never\` here; …` (`s04`).
- An aliased condition built on such an alias (`const isAdd = kind === "add"`) does the same (`s05`).
- With enum kinds on object types the re-run from the widest type already makes these reads live (`s03`).

Suggested fix: add to the related symbols the `const` aliases whose initializer is the read's variable or a property of it, as tsc does.

### 3. MEDIUM, ambiguous: an honest predicate that compares the discriminant (verified)
`isKnown(a: Action): a is Add | Say { switch (a.kind) { case "add": case "say": return true; } return false; }`, then `if (isKnown(a)) return …; const other = a;` (`s09`).
- Node and base run the residual; snap2 traps.
- Whether the predicate lied depends on whether a renamed `Add` counts as an `Add`. D-197's own reasoning says it does, which makes the predicate's `false` the only wrong answer.

Related pre-existing behaviour, the same on every compiler: after a partial discriminant test, reading the renamed instance is a checked downcast that throws (`The value narrowed to Say is not an instance of it`). That already breaks most class-literal shapes outside full switches (`s01`, `s02`).

### 4. MEDIUM: conservatism brings back a C220 compile failure (verified)
`testsValues` counts any equality on the read's variable anywhere on the paths before it, whether or not that equality helped narrow it.
- In `s11`, an unrelated `if (x === "skip") return;` turns the dead `typeof x === "number"` region live, so `flip ? new Box(x) : new Box(0)` is PTS1001 again (C237's shape).
- snap1 trapped and compiled it; the base also failed.
- Other typed uses of the boxed value compile (`s10`).

An equality between the read itself and a literal expression is a real check of the value. Only non-literal operands typed with literals or enums, and discriminant properties, are unsound.

### 5. MEDIUM-LOW: compile cost grows with flow-graph size times dead reads (verified, callgrind)
| program | snap2 vs base |
|---|---|
| `sql_query_parser` | +0.45% (two pairs) |
| one function, 150 dead typeof regions + 150 enum defaults | +3.8% (7.60G vs 7.32G; snap1 7.32G) |
| the same at 285, just under tsc's own TS2563 limit | +12.0% (39.4G vs 35.2G) |

- Each never read walks up to 4,096 flow nodes, and the re-run from the widest type walks the graph again.
- Past the cap, reads count as value tests: 273 of the 285 regions are traps and the rest are boxed. The program still matches Node.
- Caching which symbols are value-tested per function would remove the repeated walks.

### 6. LOW gap: F4's fix doesn't cover generic instances (verified)
Instance-dead reads (D-195) in the same positions — `for` condition and update, case label, past `?.` — are still `PTS1001 converting string to number` on all three compilers (`s26`). `narrowed()`'s instance branch returns the declared value rather than boxing it.

## Checked and found right
- **`testsValues`' walk** sees `a.kind` value tests through:
  - closure starts, `try`/`finally`, labelled breaks, `for…of` with `continue`, generators and async (`s07`);
  - shorthand, array and tuple literals (`s12`);
  - `a?.kind`, `case CONST:` and `switch (true)` (`s08`).
- **An assignment from a never source** doesn't narrow its target in tsc, so it's no hole.
- **`typeof` or `Array.isArray` on a class discriminant** can't arise: PTS rejects redeclaring a field with another IR type.
- **F4's boxed path under lies** (`s20`): the `for` condition and `do…while` match Node; typed uses throw the checked conversion's TypeError (D-197's intentional category for lies).
- **`PtsFlowTypeOfReference`**: the loop cache keys include the declared type; `flowTypeCache` holds only expression types. The depth limit is a theoretical risk: `flowAnalysisDisabled` is sticky outside `checkSourceElement`. In practice snap2 and the base hit TS2563 at the same size (285 is fine, 286 fails).
- **emit-c over 1,116 programs, snap2 vs base:**
  - The C differs only for the new or changed tests and `reactive_signals`.
  - No program changed its error output, and there are no PTS9001s.
  - The C for `never_functions_dispatched` is identical to the base's.

## Test quality
The guards in `narrowed_never_value_tests` miss two things: the discriminant aliases from finding 2, and an index use in an enum default from finding 1.

## Verdict
Not ready. Findings 1 and 2 must be fixed; findings 3 and 4 should be decided and logged; findings 5 and 6 can be logged as known issues.

The working tree has uncommitted parent edits (`narrowing_tests.go`, a new untracked `narrowed_dead_plain_booleans.pts`); I didn't touch or review them.

Everything is in `/tmp/h09b4b2/review2.tar.gz`: probes in `p/r1/` and `p/r2/`, outputs in `out/<name>/{node,snap2,snap1,base}.txt`, `out/r1-summary.txt`, `emitc/diff-summary.txt`, `cg/counts.txt` and its generators. It holds no binaries or C files, and I wrote the marker `/tmp/h09b4b2/review2.done`.

## Round 3 (round-2 fix `3cf15a955`, snapshot `snap3`)

Archive: [h09b4b2-review3-probes.tar.gz](h09b4b2-review3-probes.tar.gz) (sha256 `47f0fcd426254e17…`, 5,030 entries: probes in `p/r1`, `p/r2`, `p/r3`; outputs in `out/<name>/{node,snap3,snap2,base}.txt`; `out/prev-summary.txt`; `emitc/diff-summary.txt`; the cost programs and their counts in `cg/`).

---

# H09b4b2 review round 3: commit `3cf15a955` is ready for correctness, with one compile-cost finding to log

I found no wrong output, trap or compile failure that the base doesn't also have. The one notable problem is compile time: the new alias handling can make the compiler 1.85 times slower on interpreter-style code. That should be recorded and fixed, but it doesn't change behaviour. Everything below was run with Node (harness style), snap3, snap2 and the base, unless noted.

## (1) All earlier probes on snap3
- **Round 1 and round 2 together:** 62 probes. 40 match Node.
- **Matching the base instead:** 20 more, all pre-existing behaviour (PTS1001s, boundary checks, C239's `undefined`, downcast throws).
- **Matching neither:** only 2, both decided lies: `s09` (the honest-looking `a is Add | Say` predicate, F3) and `s20` (lies reaching non-first positions).
- **Round-2 findings now fixed:**
  - the discriminant aliases `s04` and `s05` match Node;
  - the enum array indexes `s21`, `s22`, `s24`, `s25`, `s27` match Node and the base;
  - `s11` and `s26` now compile.

## (2) Attacks on the new pieces

**The literal-comparison exemption is sound** (`t04`, verified). Comparing the variable itself with a literal relies on the re-run from the widest type, and that re-run covers every declared type that holds literals or enums. I tried:
- enums through aliased conditions (`const isAdd = c === Kind.Add`);
- a `const ADD = Kind.Add` alias;
- an enum destructured from a holder;
- template-literal and negative-literal unions from casts;
- booleans.

All match Node. A class union can't be compared with a literal without a TS error, so classes can't use the exemption. One small conservatism: `x === SKIP` (a const holding a literal) still counts as a value test, so C237's `?:` over boxes there is PTS1001, the same as the base (`t06`).

**`aliasedPart` covers the alias forms tsc narrows through** (`t03`, verified). Each of these matches Node and the base (snap2 trapped six of the seven):
- `const k = a["kind"]`
- `const { kind: k } = a`
- an alias declared in an outer function and tested in a closure
- `const kind = a?.kind`
- `const kind = a!.kind`
- an alias of an alias
- aliased conditions over a property alias

`let` aliases and nested destructuring aren't narrowed by tsc, so missing them is harmless.

**`isElementKey` works for single-type keys** (`t01`, `t07`). Writes, compound assignments, `++`, `??=`, destructuring-assignment targets, `delete`, typed arrays, number and string records, `in` and nested keys all match Node and the base.

One small step back from snap2 (`t02b`): a key whose declared type is a union, such as `Kind | "none"` or `"up" | 3`, used on a record. snap2 boxed it and ran correctly; snap3 reads it as its declared type, which gives `PTS1001: a key of type number | string`, the same as the base. Suggestion: read as the declared type only where that type can be a key; otherwise box it.

**Instance boxing (F6) caused no regressions** (`t08b` and the emit-c comparison). An instance-dead record key in a `for` update matches Node. An instance-dead array index in that position is PTS1001 on every version (`t08`), a remaining gap that isn't new.

**Emit-c over 1,121 programs, snap3 vs base:**
- The C differs only for this slice's tests and `reactive_signals`.
- No new failures, no change in error output, no PTS9001s.
- The one other difference, corpus `functional/transducers.pts`, comes from pre-existing nondeterminism. The emitted C randomly includes or leaves out `pts_object_take_order` for the `{ ...r, value }` spread (the base did so in 2 of 6 runs, snap3 in 1 of 7). The output still matched Node. This is worth a ledger entry: it makes C comparisons noisy, and with a different source key order the output itself could vary between builds.

## Compile cost: MEDIUM (verified with callgrind)
`mentions` now expands every const it meets in every equality on a read's paths, and nothing is cached. This happens for each read that only kind tests narrowed to never (an exhaustive typeof chain ending in `assertNever`), so the cost is roughly reads × equalities × size of the const's initializer.

| program | snap3 | base | change |
|---|---|---|---|
| dispatcher (`alias_dispatcher.pts`): a 300-key const opcode table, 150 `code === OPCODES.OPn` tests, 20 exhaustive typeof checks | 2.64G | 1.43G | +85% (wall 0.46 s vs 0.30 s) |
| `alias_cost.pts`: a 3,000-key object, 100 conditions, 100 dead reads | 8.8 s | 0.81 s (snap2 0.90 s) | about 10x |
| a 1,000-entry numeric array, or const arrows that call each other | — | — | no measurable change |

The object-key cases are slow because every identifier key in the initializer costs a symbol lookup.

tsc itself only treats two forms as discriminant aliases: an access-expression initializer (`const k = a.kind`), and destructuring from an identifier or access expression. Two fixes:
- restrict `aliasedPart` to those two forms, which never expands object literals or function bodies;
- cache the result per alias symbol.

Separately, generic-heavy code pays for the new `isDeadRead` call on every instance read in `narrowed()`:
- `generic_optional_chain_calls`: +2.3–2.7% (654.0M twice vs 636.8–639.6M; snap2 642.3M);
- `generic_interface_methods`: +0.1%;
- `sql_query_parser`: unchanged from round 2.

## (3) Test quality
- The five new tests match what they claim:
  - `narrowed_never_enum_indexes` and `narrowed_never_discriminant_aliases` are guards built from my probes;
  - `narrowed_dead_plain_literal_tests`, `narrowed_dead_plain_booleans` and `generic_narrowed_dead_unit_positions` turn earlier PTS1001s into running code.
- Two things nothing guards against:
  - the alias-expansion cost (a dispatcher-shaped compile-time check would catch it);
  - union-typed record keys (`t02b`).

## Verdict
The commit is ready for correctness: no wrong output or new trap, and no compile failure the base doesn't also have. Before closing the slice, record the compile-cost finding as a known issue, or fix it by restricting `aliasedPart` to tsc's two alias forms. The union-key step back from snap2, the instance array-key gap and the transducers nondeterminism are low and can be logged.

The working tree has uncommitted parent edits (docs and a new `h09b4b2.md`); I didn't touch them.

Everything is in `/tmp/h09b4b2/review3.tar.gz`: probes in `p/r1`, `p/r2`, `p/r3`; outputs in `out/<name>/{node,snap3,snap2,base}.txt`; `out/prev-summary.txt`; `emitc/diff-summary.txt`; the cost programs and their counts in `cg/`. It holds no binaries or C files, and I wrote the marker `/tmp/h09b4b2/review3.done`.

## Round 4 (round-3 fix `27d1f0b07`, snapshot `snap4`)

Archive: [h09b4b2-review4-probes.tar.gz](h09b4b2-review4-probes.tar.gz) (sha256 `01e2d68311b48ffc…`, 4974 entries: probes in `p/r1` to `p/r4`, outputs in `out/<name>/{node,snap4,base}.txt`, `out/prev-summary.txt`, `emitc/diff-summary.txt`, the cost programs and runners with their counts in `cg/`). Parent's note: snap4 was taken before `narrowed_never_union_keys.pts` was written, the one test file its `pts/tests/run` lacks (`diff` of the two listings); its code equals `27d1f0b07`'s.

---

# H09b4b2 review round 4: commit `27d1f0b07` is ready

The alias restriction fixed the round-3 cost problem, and I found no regression. All runs compare Node (harness style), snap4 and the base, unless noted.

## (1) All round 1–3 probes, plus two new ones
- **Probes run:** 75.
- **Match Node:** 50.
- **Match the base:** 20, all the known pre-existing cases (PTS1001s, boundary checks, C239's `undefined`, downcast throws, C237's shape in `t06`).
- **Match neither:** 5.
  - `s09` and `s20` are the decided lies.
  - `t02` is strictly better than the base. snap4 fails only on the `null | number` array index (`PTS1001: indexing an array with a value`), where the base fails on that and both record keys.
  - `t08` fails on the same instance-dead array index as the base, with different wording (`with a value` instead of `with a string`). This is the gap I logged in round 3.
  - `u02` is the commit's own `narrowed_never_union_keys.pts` (from my `t02b`). It matches Node; the base fails with PTS1001.

## (2) The alias restriction doesn't reopen anything
- `t03` matches Node and the base in every form: `a["kind"]`, `{ kind: k }`, an alias in an outer function, `a?.kind`, `a!.kind`, an alias of an alias, and aliased conditions over a property alias.
- `s04` and `s05` also match Node and the base.
- A new probe at the edges of the restriction (`u01`) matches Node and the base: `const kind = (a.kind)`, `const kind = a.kind!`, `const { kind } = h.a`, `const { kind } = (a)`.
- `t01` (element-key positions) and `t07` (key writes) still match Node and the base.

## (3) Compile cost, callgrind instruction counts vs the base
| program | snap4 | base | change | round 3 (snap3) |
|---|---|---|---|---|
| `alias_dispatcher` | 1,492.8M | 1,419.6M | +5.2% | +85% |
| `alias_cost` | 2,146.7M | 2,091.3M | +2.6% | about 10x in wall time |
| `alias_chain` | 631.9M | 627.5M | +0.7% | |
| `generic_optional_chain_calls` | 642.1M | 639.1M | +0.5% | +2.5% |
| `generic_interface_methods` | 671.9M | 673.7M | −0.3% | |
| `sql_query_parser` | 641.1M | 648.1M | −1.1% | |
| `big_function_150` | 7,656.0M | 7,306.5M | +4.8% | the recorded F5 cost |

- Run-to-run noise is about ±2%.
- About one valgrind run in six printed no count, even when run alone. I re-ran those until each produced a number.
- Wall times for the other cost programs (`alias_realistic`, `alias_functions`, `alias_functions_big`) are now in line with the base.

## (4) Emit-c over all 1,122 programs vs the base
I used the commit's own test tree, taken with `git archive 27d1f0b07`. snap4's `pts/tests/run` is missing about 10 test files, including the new `narrowed_never_union_keys.pts`. Its code directories do match the commit byte for byte.
- **Differences:** only this slice's tests (now including `narrowed_never_union_keys`) and corpus `reactive_signals`.
- **Not a regression:** corpus `transducers.pts` differs too, but that's the pre-existing nondeterministic `pts_object_take_order`. This time the base's C had it and snap4's didn't, the reverse of round 3. Its output matched Node in every run.
- **No failures:** no new compile failures, no change in error output, and no PTS9001s.

## Verdict
Ready. Small items that can be logged as known issues:
- the instance-dead array index (`t08`) and the `null | number` key (`t02`), both still PTS1001 as on the base;
- C237's shape after `x === SKIP` (`t06`);
- the `transducers.pts` emit-c nondeterminism.

The working tree has uncommitted parent edits (docs, `DECISIONS.md`, a new `h09b4b2.md`); I didn't touch them.

Everything is in `/tmp/h09b4b2/review4.tar.gz`: probes in `p/r1` to `p/r4`, outputs in `out/<name>/{node,snap4,base}.txt`, `out/prev-summary.txt`, `emitc/diff-summary.txt`, and the cost programs and runners with their counts in `cg/`. It holds no binaries, no C files and no copy of the extracted test tree, and I wrote the marker `/tmp/h09b4b2/review4.done`.
