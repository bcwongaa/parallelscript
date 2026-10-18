# H09b1 reproducer report (opus-reproducer, claude-opus-5-5, xhigh)

Saved by the parent from the child's final message, unedited below the rule. Probes: [h09b1-reproducer-probes.tar.gz](h09b1-reproducer-probes.tar.gz) (`INDEX.txt` inside). Paths in the report are relative to the archive's `work/` (unpacked under `/tmp/h09b1-repro/work/` in the session); `W` is the reproducer's worktree of `64564a9a2`, removed by the harness afterwards.

---

# H09b1 reproducer report: generic methods through interfaces and object literals (C169, first half)

**Summary.**
- **RED confirmed.** All three slice forms throw `TypeError: Expected Box<…>, got Box<any>` on HEAD `64564a9a2`, where Node prints the box.
- **Matrix.** Your count of 36 + 36 TYPEERR is correct, but only 18 of those 72 are calls in this slice (9 interface-method, 9 object-literal). The other 54 are method values (`im.mk` / `o.mk` passed on), which belong to later H09b slices.
- **Prototype.** About 190 lines across lowering, cgen and the runtime. It makes 46 of the 53 inventory lines with a Node comparison match, and all 18 slice cases in the matrix. Nothing changes for the worse in the 754-case matrix. The broad suite is green with it (`TestRun`, `TestRunMatchesNode`, `TestCorpusMatchesNode`, `TestCompilerFailsCleanly`).
- **One regression risk, verified.** If the new by-name lookups are not limited to the first build (`AllInstances`), a program that compiles on HEAD fails with PTS1001 + PTS9001, exit 3.
- **Tests.** Two RED tests and one `no-diff` guard are ready and checked.
- **Adjacent.** Six defects outside the slice, each with a repro.
- I didn't commit or push anything, and the worktree is clean (`git status` empty, HEAD `64564a9a2`).

## 1. RED on HEAD

**Setup**
- Worktree `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a7e1d288759a50a05`, HEAD `64564a9a2`. All five submodules were initialised from local references.
- `GOMAXPROCS=2 make build` exited 0. CLI sha256 `68667dd66711c1ee301e179678a64e6a7a54d939f5a64e12978cb3e427935ea7`.
- Node v24.21.0, run on `.mts` copies with `--no-warnings --experimental-transform-types` and `TZ=America/New_York`, as the harness does.
- Command: `./cmp.sh W red/*.pts` (output in `red/red-head.txt`).

| Probe | HEAD (exit) | Node (exit) |
|---|---|---|
| `r01_slice.pts` (the first three cases of `generic_method_factories.pts`) | `interface call of a class's method TypeError Expected Box<number>, got Box<any>` / `object literal's method TypeError Expected Box<number>, got Box<any>` / `…through an interface TypeError Expected Box<string>, got Box<any>` (0) | `… 6 true` / `… 6` / `… 2` (0) |
| `r02_uncaught.pts` (`const m: Maker = new ClassMaker(); const b: Box<number> = m.make(5)`) | `Uncaught TypeError: Expected Box<number>, got Box<any>` at line 14 (1) | `Box { value: 5 }` (0) |
| `r03_literal_uncaught.pts` (`lit.make(5)`) | same TypeError, line 10 (1) | `Box { value: 5 }` (0) |
| `r04_literal_iface_uncaught.pts` (`const lm: Maker = {…}`) | `Expected Box<string>, got Box<any>`, line 13 (1) | `Box { value: 'ab' }` (0) |

## 2. Inventory: HEAD vs Node, and the prototype

"T" means `TypeError Expected X<…>, got X<any>` (or `got Array`/`got Map` for array, tuple and Map results). Every T line prints a value under Node.

| Form (probe) | HEAD | Node | Prototype |
|---|---|---|---|
| **Interface calls on class instances** (`i01`) | | | |
| method declared in the class; inherited from a base; overridden in a subclass (through the interface, and through the base class); a base instance of an overridden method; abstract base through the interface; a class with two interfaces; generic interface `PairMaker<U>` with `make<T>(x: T): Pair<T, U>`, by a plain and by a generic class; interface extending another | T on every line (`Pair<number, string>` vs `Pair<any, string>`) | values | match |
| abstract base called through the base (dispatched) | `6` | `6` | `6` |
| **Receivers** (`i02`) | | | |
| union of interfaces | T | `6 2` | match (also needed a change in `unionMethodCall`) |
| `?.` (`maybe(true)?.make(5)`) | `Expected Box \| undefined, got Box<any>` | `6 undefined` | **still T**: adjacent C-a below |
| parameter; array element; field; `Map` value; `(a as Maker)` from `any` | T | `6` | match |
| union of a class and an interface (`i02b`) | **PTS1001** `converting CB \| ClassMaker to ClassMaker \| {b(): number; make(value): value}` | `6 2` | unchanged: adjacent C-b, which happens with plain methods too |
| **Object literals** (`i03`) | | | |
| own type; behind an interface; contextually generic `make(x)`; captures a local (own type and behind an interface); uses `this` (both); calls `this.inner(x)`; a factory's literal each call; a literal in `makerFor<U>`'s instance | T on all 10 lines | values | match (10/10) |
| **Arrow properties and fields** (`i04`) | | | |
| `{ make: <T>(x) => new Box(x) }` own type; behind a function-typed property; class field arrow through the class; class field arrow behind a function-typed property | `6` (fixed by H09a) | `6` | `6` |
| a literal's **method** for a function-typed property `make: <T>(x: T) => Box<T>` | T | `6` | match |
| an **arrow property** where the interface declares a **method** | T | `6` | **still T**: the closure in the field is a `functionAsMethod` adapter, which hides the arrow's instances (§5.4) |
| a class field arrow used as an interface's method (`i04b`) | **PTS1001** `using a FieldMaker as a {make(value): value}…` | `6` | unchanged: adjacent C-c, plain methods too |
| **Mixed array** (`i05`): 2 classes and 2 literals in one `Maker[]` | T ×8 | values | match |
| **Result shapes** (`i06`): `Box<T>`, `Box<T>[]` (`Expected Box[], got Array`), `[Box<T>, T]` (`Expected [Box, number], got Array`), `Box<T> \| undefined`, `Map<string, Box<T>>` (`got Map`), `Promise<Box<T>>` through `await`; each via class-behind-interface, literal-behind-interface and literal own type | T on all 21 lines | values | match (21/21) |
| **D-155 boundaries** (`i07`): class lies through `any`, through the interface and through the class; a plain object returned for a `Box`; literal lies; class or literal through `any` called with no type arguments | throws (`got Box<string>`, `got Object`, `got Box<any>`) | wrong-typed values | **unchanged, still throws**; the dynamic-result line stays `6 true` |
| **Signatures** (`i12`): two interfaces with one method name and different signatures; an extra defaulted type parameter; a generic class's instance behind the interface; an interface call inside a generic function's instance; a structurally compatible class | T ×5 | values | match |
| **Polymorphic recursion** through an interface (`i08`) | T at run time (compiles) | `6` | ungated prototype: **PTS1001 + PTS9001, exit 3** (a regression); gated: T, same as HEAD (the build falls back) |
| polymorphic recursion through a literal's `this` (`i09`) | T | `6` | T (no regression) |

## 3. The H09 matrix on HEAD (2 jobs, absolute case paths)

- **The two families on HEAD** (`matrix/results-head.json`):
  - `iface_method`: 36 TYPEERR, 4 OK, 2 TSERR, 1 DIAG.
  - `object_method`: 36 TYPEERR, 4 OK, 2 TSERR, 1 DIAG.
  - This confirms your 36 + 36.
- **What the 72 TYPEERR are:**
  - **18 are calls in this slice** (uses `direct` and `explicit`). Per family that is `direct` for the `box`, `array`, `tuple`, `union`, `nested`, `map`, `mapbox` and `promise` shapes, plus `box/explicit`. `shape__*__direct` is already OK because a `{value: T}` result converts by copy.
  - **54 are method values**: `param`, `var`, `field`, `iface_sig`, `through_decl`, `through_value`, `map_ctx`. The generator's `mk` is `im.mk` / `o.mk`, passed on as a value. These belong to later H09b slices.
  - The `bind` DIAG and the `call`/`apply` TSERR are artefacts. The case-by-case list is in `matrix/classify-head.txt`.
- **Full 754 on HEAD:** OK 538, TYPEERR 140, DIAG 2, TSERR 74. This matches your post-H09a numbers.
- **Full 754 with the prototype:** OK 556, TYPEERR 122. All 18 changes are TYPEERR → OK, all of them `iface_method`/`object_method` with `direct`/`explicit`. Nothing got worse, and every OK case printed the same output (`matrix/compare-full-head-proto.txt`).

## 4. Proposed tests (`tests/`, checked with `check.py --pts … --home W`)

| Test | HEAD | Node | Prototype |
|---|---|---|---|
| `generic_interface_methods.pts` (11 lines: declared, inherited, overridden, abstract, two interfaces, generic interface, extends, union of interfaces, five receivers, four result shapes, a promise) | **FAIL**: all 11 lines are C169 TypeErrors | MATCH | PASS |
| `generic_object_literal_methods.pts` (12 lines: own type, behind an interface or contextual, captures, `this`, `this.inner`, factory, generic function instance, method for a function-typed property, result shapes own and behind an interface, classes and literals in one array, a promise) | **FAIL**: all 12 lines are C169 TypeErrors | MATCH | PASS |
| `generic_method_any_boundaries.pts` (`no-diff` guard: five lies plus two calls through `any` must throw; dynamic results work) | PASS | differs, as intended | PASS |

- **Your call:** "union of interfaces" in the first test needs the `unionMethodCall` change. Drop that line if your fix leaves union receivers out.
- I left `?.` (C-a) and the arrow-property-for-a-method form (§5.4) out of both RED tests.
- **The existing skipped test:** with the prototype, the first three lines of `generic_method_factories.pts` pass. The method value, `bind` and both non-generic-context lines still throw. Splitting those lines out, or re-skipping the file with a narrower cause, would fit this slice.

## 5. Root cause and fix sketch (grounded in the code; the prototype is `proto/h09b1-proto.patch`)

**5.1 What HEAD does**
1. An interface with a generic method gets the erased signature (`interfaceMethod`, objects.go:243), which also calls `wantInMethodTables(name)`. Every class's generic method of that name then puts its erased copy into the method table (`addErasedMethod`).
2. `interfaceMethodCall` (objects.go:1062) emits a plain `ir.InterfaceCall`. In C, `pts_method_find`/`pts_method_find_adapted` finds either the class's `…_dynamic` erased copy, or, for a shape, `pts_entry_S_make`, which reads the closure from the field. Its `new Box(x)` makes a `Box<any>`, and `fromErased` rejects it.
3. The literal's own-type branch calls `CallValue{FieldRef}` on that same erased closure.
4. **Why no instance is ever found:**
   - D-148 demands are only made by `instanceValueCall` (valueCall) and `dispatchedInstanceCall`. `interfaceMethodCall` never makes one.
   - `objectMethod` (methods.go:15) lowers a literal's generic method under `l.erasing(...)` as a plain closure. It is never a `genericFunc`, never goes through `addErasedValue`, and has no instance table.
   - `pts_instance_find(obj, name, sig)` walks only the `type->instances` of the receiver's class chain. A shape descriptor has no instances, and the closure in the field has its own descriptor.

**5.2 The prototype**
- **Demands by name.** `demand.name` is new. `namedInstanceCall` follows `dispatchedInstanceCall`: same `holdsGenericObjects` test on the method type's signature, `demandAt` with the erased params, and `instanceCall(d, …, receiver, name, erasedCall)`. Here `erasedCall` is HEAD's `InterfaceCall` or field `CallValue`, wrapped in `fromErased`. It is used in:
  - the interface branch and the shape branch of `interfaceMethodCall`;
  - `unionMethodCall` (7 lines).
  - It returns early under `DeclaredInstances`, so the D-190 fallback restores the base.
- **How `specialize` pairs a named demand.**
  - (a) With every non-static entry of `genericMethodList` that has that name (`specializeNamedMethod`). The entry goes on `g.class.Instances`, with type arguments inferred by `PtsInstantiateSignatureInContextOf(sig(g), d.signature)` rather than by position, so generic interfaces and generic classes work. If there is no instance, the entry has no function, which ends the search, as D-148 does.
  - (b) With every erased value whose `method == name` and whose erased signature after `this` equals the call's (`specializeLiteralMethod`). The instance goes on the closure's `Instances` under the method's name, using `fits: fn.Shares == v.fn && fitsInstance(fn.Params[1:], …)`.
  - Value demands skip method values (`v.method == ""`).
- **Literal methods become generic closures.** In `objectMethod`, a generic method declaration (in `AllInstances` mode, not in erased code, `!differs`) becomes `genericFunc{node, module, parameters, outer, class: field.Class, closure: fn}` plus `addErasedValue`, the same pattern as `functionExpression` in H09a. Its instances run with the method's closure: `Shares`, `shareCaptures`, `scope`.
  - A method whose field is a function-typed property, not a method field, stays a plain value. Its value calls find it through the existing D-148 path, which fixes the "method for a function-typed property" case.
- **cgen.** Named entries change convention to `(pts_closure *env, pts_object *self, args…)`:
  - `instanceTable`: env is passed through for closure instances.
  - `instanceCall`: for a name it emits `pts_instance_find_method(x, name, sig, &env)` and calls `found(env, (pts_object *)x, args)`, otherwise the `Otherwise` expression.
  - The dispatched (D-101) entries switch to the same convention.
- **Runtime.** `pts_instance_find_method` in object.c (and `pts.h`):
  - First the class walk, as `pts_instance_find` does, with `*env = NULL`.
  - Then, if no class has an entry for the name: `find_field(type, name)`. The field must be `PTS_FIELD_OBJECT` and hold a closure (kind `PTS_KIND_FUNCTION`). The closure's own table is searched for a named entry, with `*env = closure`.
  - This also covers frozen twins and the shape's other property orders, because they copy the field table.

**5.3 Evidence with the prototype**
- **RED and inventory:** all four RED probes match Node. Matches as listed in §2.
- **Matrix:** the 18 slice calls become OK, nothing worse (§3).
- **Focused run tests** (your pattern, `-parallel 2`, with the three tests added to `run/`): ok, 67 PASS / 25 SKIP.
- **Broad suite** (`TestRun`, `TestRunMatchesNode`, `TestCorpusMatchesNode`, `TestCompilerFailsCleanly`, gated prototype): `ok` in 520 s. 484 + 418 + 263 PASS and no FAIL (`proto/gotest-broad-proto.log`).

**5.4 Risks and choices for the real fix**
- **(a) Gate named demands to `AllInstances`.** This is verified by `i08`: ungated, the polymorphic recursion through an interface reaches the 64-instance limit, giving PTS1001 + PTS9001 (exit 3) where HEAD compiles. Gated, the build falls back, but for the whole program (C176's granularity).
- **(b) Speculative growth (C172).** A named demand pairs with every method of that name, and shapes are interned by members, so restricting by the receiver's shape doesn't help.
  - `growth2_40` (40 literals and 40 classes, each called once with its own type): 6,031 → 42,431 lines of C, build 1.1 → 7.7 s.
  - `growth_30x30` (every pair used): 4,498 → 25,198 lines, 0.8 → 4.3 s.
  - These are wall times on a busy host. The C line counts are exact.
- **(c) On a miss, the runtime fallback does a linear `find_field` with strcmp.** This includes class receivers that have no entry. A shape flag, or only searching objects whose descriptor is a shape, would avoid the cost.
- **(d) The instance entry casts `self` to the instance's `this` shape.** This is safe only while a literal method's closure can't reach an object of another layout. HEAD keeps that true: spreading or reassigning a method is PTS1001 (x06/i11), and method values are always bound wrappers. HEAD's `pts_entry_S_*` relies on the same thing. If a later slice allows spreading methods, both places need a descriptor check.
- **(e) Arrow properties for an interface's method** (`{ make: <T>(x) => … }` as `Maker`) stay T, because `functionAsMethod` wraps the arrow in an adapter. One fix: when the value is a `MakeClosure` of an erased value with instances, give the adapter named entries that call the arrow's instances with the captured closure as env.
- **(f) The runtime unit test** `object_test.c` (lines 148–159) tests `pts_instance_find`. Extend it for the field fallback, a nil entry ending the search, and a frozen twin.

## 6. Adjacent defects (outside the slice; minimal repros in `adj/`, outputs `*-head.txt`)

- **C-a, `?.` calls of every demand kind keep the erased copy** (`x05`): value call, dispatched call and interface call (`i02` r3).
  - HEAD: `TypeError Expected Box | undefined, got Box<any>`. Node: `6`. Without `?.` the same calls print `6`.
  - Cause (verified in the emitted C): tsc's resolved signature for an outer call chain carries `undefined` in its return type (`getOptionalCallSignature`, `IsOuterCallChain`), so `want.Result` is the union `Box | undefined` and no instance fits.
  - The fix needs the signature without its call-chain flags, i.e. a small `// PTS:` checker helper that instantiates `sig.target` with `sig.mapper`.
  - My attempt through `erasedTarget` did not work and was reverted.
- **C-b, a conditional of two classes can't be returned as a union of a class and an interface.** HEAD gives PTS1001 `converting CA | CB to CA | {b(): number; make(number): number}` for plain methods too (`x02`). Node: `2 3`.
- **C-c, a class whose field is an arrow can't provide an interface's method.** HEAD gives PTS1001 `using a FieldMaker as a {make(number): number}, whose make it doesn't provide…` with a plain arrow too (`x04`). Node: `6`.
- **C-d, spreading an object literal that has a method:** PTS1001 `spreading the property make of a {tag: string; make(number): number}`. Node: `6 1` (`x06`).
- **C-e, `Object.freeze` of an object literal with a method:** PTS1001 `using a object2 as a {make: (number) => number}…`, because D-157 lowers `Readonly<X>` as X only for classes, `this` and library types. Node: `6 7` (`x06`).
- **C-f (probably a new trigger of C172), polymorphic recursion through an abstract (dispatched) generic method** (`i10`): PTS1001 `a generic class with more than 64 instances (Box)` twice, then PTS9001 nil pointer dereference, exit 3. Node: `6`. The D-190 fallback doesn't help, because dispatched demands are also made under `DeclaredInstances`.
- **Message quality (N6, already known):** `Expected Box | undefined`, `Expected Box[]`, `Expected Map<string, Box>` show no type arguments.

## 7. Cost and what I ran vs inferred

- **Ran:** everything quoted above. At most two heavy jobs at a time: the matrix used 2 jobs, go tests used `-parallel 2`.
- **Caveat:** the HEAD runs of the full matrix, `i08`–`i12` and the growth probes happened while the worktree's runtime had the prototype's one added function. HEAD's C doesn't call it, and the full-matrix HEAD counts equal yours.
- **Inferred, not measured:** the per-call cost of the extra lookup (no callgrind run) and the growth figures beyond the probes.

Files are in `/tmp/h09b1-repro/work/`; `INDEX.txt` says how to run each thing:
- `/tmp/h09b1-repro/h09b1-reproducer-probes.tar.gz` (the archive)
- `/tmp/h09b1-repro/work/proto/h09b1-proto.patch` (the prototype)
- `/tmp/h09b1-repro/work/tests/generic_interface_methods.pts`
- `/tmp/h09b1-repro/work/tests/generic_object_literal_methods.pts`
- `/tmp/h09b1-repro/work/tests/generic_method_any_boundaries.pts`