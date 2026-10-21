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
