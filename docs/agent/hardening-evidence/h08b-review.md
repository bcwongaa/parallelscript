# H08b review reports

The `opus-reviewer` child (claude-opus-5-5, xhigh) reviewed byte-checked snapshot worktrees of the change, read-only. Each round's final message is saved below as it was given; the parent's dispositions are in [h08b.md](h08b.md).

## Round 1 (snapshot `/tmp/h08b/review1`, `pts/` diff sha256 `3b23fc1c…`)

Probes: [h08b-review-probes.tar.gz](h08b-review-probes.tar.gz) (sha256 `f5e9886b…`; paths below are under `rev1/`, see `rev1/README.txt`).

**Verdict: ready.** I found nothing that blocks the slice. There is one low-severity regression that only changes diagnostic text. The `any`-callee asymmetry you asked about is acceptable for this slice and is not a regression against Node; I recommend recording it as a known gap. I made no edits to the snapshot or the main repo; the extra modified docs in the main repo are yours. Probes are in `/tmp/h08b/h08b-review-probes.tar.gz` (sha256 `f5e9886b…2dbb9`, paths below are under `rev1/`, see `rev1/README.txt`); the marker `/tmp/h08b/review1.done` exists.

### Findings, by severity

**1. Low, regression (diagnostic text only): some PTS1001 messages now name an internal shape, `objectN`, instead of the source type.** Verified.
- **Where:** `pts/lower/objects.go:612` builds the message from `source.Type()`. With a suspension, `source` is now the kept copy, whose values-only shape comes from `yield_expressions.go:500-505` / `516-539` / `542`.
- **When:** `propertiesCopy` succeeds on a source that has a property the literal's shape lacks, so the program is rejected either way, now with a worse message. H22 exists to remove `objectN` names.
- **Repro `diag/d01_private_field.pts`** (a TS-`private` field):
  - base: `` 3:76 - error PTS1001: spreading the property `hidden` of a Derived is not supported yet ``
  - snapshot: `` …`hidden` of a object2 is not supported yet ``
- **Repro `diag/d02_accessor_literal.pts`** (a literal with getter and setter):
  - base: `` …`a` of a {a: number; b: number} … ``
  - snapshot: `` …`a` of a object3 … ``
  - Node runs both programs.
- **Possible fix:** report with the original source type (keep it next to `keptNames`, or use `typeAt(part.node.Expression())`), or have `propertiesCopy` give up on names the literal's shape has no field for.
- I swept 19 diagnostic probes (`diag/d01`–`d19`); only these two changed text. No new PTS1001 or PTS9001 anywhere.

**2. Assessment: the `any`-callee asymmetry is acceptable.** Verified with `p/any_asym.pts`.

| Call | base, no suspension | snapshot, no suspension | snapshot, with yield | Node |
|---|---|---|---|---|
| `dyn(...anySet, …)` | TypeError | TypeError | `1/2/10` | `1/2/10` |
| `dyn(...anyStr, …)` | TypeError | TypeError | `a/b/10` | `a/b/10` |
| `dyn(...gen(), …)` | TypeError | TypeError | `1/2/10` | `1/2/10` |
| `dyn(...typedSet, …)` | TypeError | TypeError | TypeError | `1/2/10` |

The TypeError is `Cannot spread X into the arguments of a call through any`. In the base, the suspended form threw it too.
- Every change moves toward Node and toward SPEC line 191, which already says spreads through `any` iterate sets, strings and generators. No case got worse.
- The runtime gap (`pts_spread_length`, `pts/runtime/src/dynamic.c:741-754`, arrays, tuples and typed arrays only) is pre-existing.
- The inconsistency left over: a dynamic value works only when a suspension follows, and a typed Set, Map or string never works.
- **Recommendation:** record it in the decision's known gaps. The consistent fix is to iterate non-array spreads in `dynamicCallWith` (`dynamic.go:172-177`) or in the runtime for both paths. That fixes the typed sources too, so it fits H08c or H29 better than this slice.

**3. Info: snapshot copies stay alive in the frame.** A kept copy lives in the generator frame until the slot is overwritten or the frame dies. Before, the frame held only a reference to the original. This follows D-109's design (every local lives in the frame) and only matters for a large spread in a long-lived generator.

**4. Test quality and bookkeeping**
- Each new test's expect block equals Node's output exactly.
- On the base they fail by output only, on most lines (7/7, 32/34, 31/31, 14/15, 9/10); the lines that match are printed console output or the deliberate guard cases.
- Each cause is pinned:
  - classification: const names, `f([...xs], S)`, `f({ ...o }, S)`, `[...(yield), ...(yield)]`;
  - every consumer;
  - all three receiver-evaluated-twice paths.
- **Gaps worth pinning** (all verified correct now):
  - D-108 order through the copy: `{ ...p, a: yield }` where `p` came from `{b, a}` (`p/order_d108.pts`).
  - An optional property left out and set while suspended (`p/optional_props.pts`).
  - Tuple receivers `pair.values()`: PTS1001 with a suspension on the base, now compile (`p/match_typed.pts`).
  - A union-of-arrays receiver: PTS9001 with a suspension on the base, now matches Node (`p/union_recv_susp.pts`).
  - A throwing getter or iterator now throws before suspending, as in Node (`p/throwing_spreads.pts`).
- **Bookkeeping:** the skip line of `spread_elements_read_before_later_arguments.pts` cites H08c and C152, which aren't in the snapshot's plan or ledger yet.

**5. Pre-existing problems found on the way** (verified on the base, not caused by this change)
- `[...(number[] | string[]).values(), 10]` without a suspension hits PTS9001 while emitting C (`ir.Type is *ir.Union, not *ir.Array`). Repro `p/union_recv_sync.pts`. The suspended form used to ICE in lowering and now works.
- An interface-typed spread of a class instance copies a prototype getter (`{"a":1,"b":2,"z":0}`), where Node gives `{"b":2,"z":0}`. Repro `p/mixed_iface2.pts`.
- `{ ...defaults, ...o }` keeps the default when `o.b` was set to `undefined`; Node gives undefined. The comment on `spreadOver` documents this. Repro `p/optional_props.pts`.
- Spreading a class with a TS `private` field is PTS1001, though JS copies the field (`diag/d01`).

### What I verified and found correct
- **Snapshot tests:** all six run against Node; five match, and the skipped cause-D test differs exactly as its skip line says (`tests/`).
- **No regressions outside suspensions:**
  - emit-c gives byte-identical C for 520 of the 525 run tests (only the 5 new tests differ);
  - all 224 corpus/examples programs that compile give identical C (the 20 that fail, fail the same way);
  - spreads after the suspension give identical C (`sfirst/`).
- **Go and harness:**
  - `go test ./pts/lower/ ./pts/cgen/` passes;
  - `go vet` and `gofmt` are clean;
  - a harness subset of 33 suspension, generator and spread tests passes (`harness_subset.txt`).
- **GC and frame safety (D-109):** I read the generated C.
  - The kept snapshots are `pts_frame->kept*` members in generators, async functions and top-level await.
  - The `Bind` temporaries (`tuple`, `spread`) are statement-expression C locals that never span a yield (`p/gc_frame.c`, `p/gc_async.c`).
  - A stress run with 3,000 suspended generators and heavy allocation between resumes came out intact (`p/gc_stress.pts`).
- **Semantics matching Node, or matching the same program without a suspension where PTS differs from Node by design:**
  - holes (D-183/D-085);
  - optional tuple elements (consistent with the known pre-existing defect);
  - own, inherited and `#private` class fields; unset fields (D-141);
  - D-108 order; Mixed interface fields; getter order, run once each before the suspension (`p/getter_order.pts`);
  - numeric-key Records;
  - each kind of call-valued source evaluated once, receivers included (`p/once.pts`);
  - erased generics, rest parameters, `super.m(...)`, `yield* [...xs, yield]`, `[...this, yield]`, templates;
  - match arrays with undefined groups (the `ArraySlice` copy keeps them);
  - whole literals kept and converted to other parameter types.
- **Matrix subset:** 13 programs run through the reproducer's `matrix.py only` against the snapshot. No case is wrong because of the suspension; the remaining WRONG, DIAG and ICE results all fail their controls too (`mx/only_*.txt`). The reproducer's explore probes behave the same, apart from the pre-existing items you listed as out of scope.
- **Compile time:** emit-c of the iterables test takes 0.10–0.11 s against 0.09–0.10 s on the base, and the C grows from 4,517 to 4,559 lines.

## Round 2 (snapshot `/tmp/h08b/review2`, `pts/` diff sha256 `dcd99152…`)

Round 1's reviewer, resumed with its probes, time-boxed to the delta. Probes: [h08b-review2-probes.tar.gz](h08b-review2-probes.tar.gz) (sha256 `f03d52f2…`; paths under `rev2/`, see `rev2/README.txt`).

**Round 2 verdict: ready.** Finding 1 from round 1 is fixed, the new tests are sound, and nothing else changed behaviour. The docs need a few corrections and four rows are missing from the ledger (details below).

`/tmp/h08b/review2/pts-bin/pts` didn't exist when I started, so I built my own copy at `/tmp/h08b/rev2/pts-bin/pts` with `go build -o`, outside the snapshot. The snapshot's `git status` is the same as at the start. Archive: `/tmp/h08b/h08b-review2-probes.tar.gz` (sha256 `f03d52f2…46ab6`, see `rev2/README.txt`); marker: `/tmp/h08b/review2.done`.

### The delta, verified
- **The `objectN` fix works.** On the new errors test `pts/tests/errors/object_spread_suspension.pts`:
  - the base and round 2 print the same three messages (`of a Derived` ×2, `` `a` of a {a: number; b: number} ``);
  - round 1 printed `of a object3` and `of a object4` at 18:32 and 19:32.
  - So the test is red on round 1 and green on the base and round 2. The harness checks that each expected line is contained in the output, and the text matches exactly as printed from that directory.
- **No other diagnostic can name the copy.** The one message in `spreadObject` now uses `part.of`. The Record-mismatch message in `spreadIntoRecord` prints a Record copy, which keeps the source's own type. Small nit: `copiedNames` always returns a type when it succeeds, so the `of == nil` fallback in `spreadObject` never runs.
- **`gofmt` and `go vet ./pts/lower/` are clean,** and no `keptNames` is left.

### Re-running the round-1 probes
- **Probes:** all of `p/`, `diag/`, `explore/` and `sfirst/` ran with both the round-1 and round-2 CLIs: 60 give the same output and 4 changed. All four changes are the `objectN` message: `d01`, `d02` and the same two cases in `p/diag_private_field` and `p/mixed_iface`, which now match the base's text.
- **Emitted C:** for every `run/` and `errors/` test, round 1 and round 2 give identical C. 526 compile and give the same C, 92 fail with the same diagnostics, and the only difference is the new errors test's diagnostic.
- **Run tests against Node:** the seven run tests give Node's output, and every expect block is exactly Node's. The skipped cause-D test still differs from Node, as its skip line says.

### The new `run/suspension_spread_copies.pts`
- It matches Node, and its expect block is Node's output.
- On the base it stops at PTS1001 (`` calling `pair.values` ``).
- **Base variant without the receivers:** 5 of its 7 lines fail: left out, both throws, await order and await left out. You said "5 of 8"; without the receivers line there are 7.
- **Lines that pin something:**
  - The receiver lines would show `…/100/100/…` and `…/3/4/…` if the receivers were read after the suspension.
  - The await left-out line tells 5 from 6.
- **Lines that are guards only, as its header says:**
  - the order lines (they catch a dropped `OrderOf`);
  - the elements line: the match and typed-array view elements are written before both yields.

### Docs, checked against what I verified
**D-188** is accurate, with two corrections:
- **Object spreads:** it says the copy "copies neither a class's methods nor prototype getters". That holds for a class-typed source. An interface-typed source holding a class instance still reads the class's prototype getter, before and after this change: `p/mixed_iface2.pts` gives `{"a":1,"b":2,"z":0}`, Node `{"b":2,"z":0}`. It belongs in the known gaps.
- **C158** (D-188's known gaps and the ledger row) is narrower than what I saw. The interface's order also wins when the literal adds nothing but the object behind the interface is a class instance: `{ ...fromClass, a: 10 }` gives `{"a":10,"b":2}`, Node `{"b":2,"a":10}` (`rev1/p/order_iface.pts`, line 1). D-108 only takes the order between objects of the same shape. Widen the row rather than adding one.

**C153** (the `any`-callee asymmetry) is correctly recorded and sent to H08c, in D-188, the ledger and the H08c row. SPEC §3.4 is the right section (line 191 sits under it). You could add that a typed Set, Map or string throws with or without the suspension, since only dynamic values, objects and generators get drained.

**C139, C152 and C154–C157** match what I observed, and the H08c row is accurate.

**Your list of my pre-existing items, checked against the ledger:**

| Item | Ledger row? | What it needs |
|---|---|---|
| Union-receiver PTS9001 without a suspension | none | A new row. `[...(number[] \| string[]).values(), 10]` and `.keys()` hit PTS9001 while emitting C (`ir.Type is *ir.Union, not *ir.Array`), and `.entries()` hits it while lowering. Repros `rev1/p/union_recv_sync.pts` and `rev1/p/union_receiver2.pts`. The suspended form ICEd on the base and now works (`union_recv_susp.pts`, also pinned in `suspension_spread_copies`). |
| Interface spread of a class instance copying a prototype getter | none | A new row (`rev1/p/mixed_iface2.pts`, base and H08b alike). |
| TS `private` field spread is PTS1001 where JS copies the field | none | A new row (`rev1/diag/d01_private_field.pts`, base and H08b alike). |
| `{ ...defaults, ...o }` keeps the default for an explicit undefined | none | Not a gap: D-065 chose it ("where the result's field can't hold undefined … an undefined one does too"). A row is optional, as an accepted divergence from Node (`rev1/p/optional_props.pts`). |

So three of the four lack a row and need one; the fourth is D-065's documented choice.
