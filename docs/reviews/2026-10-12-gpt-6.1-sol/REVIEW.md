# ParallelScript independent review: M0–M5

Snapshot: detached `bcd32a94d`, 2026-10-12. References are relative to this worktree. **No tracked files were changed; no commits, pushes, tags, or access to the builder checkout.**

## Verdict

ParallelScript is a substantial working compiler/runtime, not an AST-to-C demonstration.
Its typed IR, descriptor-driven dynamic checks, native GC, parallel libraries, and differential harness are strong foundations.
The fresh PTS suite passes, and the headline parallel/synchronization APIs work in sampled programs.
Nevertheless, four small, accepted programs reproducibly segfault, including a cross-loop operation.
Promise/iterator/suspension semantics also diverge from Node; milestone completion is ahead of semantic completion.
Several deliberate decisions narrow locked requirements and deserve explicit owner ratification.
Preserve the implementation, but add safety/compatibility stabilization before treating multicore HTTP or M7 “usable” as acceptance gates.

## Verification

Commands ran with `export GOMAXPROCS=3 PTS_CACHE_DIR=$HOME/.cache/pts-review PTS_HOME=$PWD PTS_THREADS=2`:

- `go build -o /tmp/pts-review ./cmd/pts`: success.
- `go test -count=1 -p 2 ./pts/... ./cmd/pts/`: exit 0; **nine tested packages passed**, including `pts/tests` in **317.973 s**. Log: `/tmp/pts-review-baseline-20261012.log`.
- `go test -count=1 -p 2 ./internal/parser -run '^TestParallel'`: `ok …/internal/parser 0.004s`. No full upstream suite or benchmark reruns.
- Fifteen new `.pts` probes in `/tmp/pts-review-progs/`; sequential builds, five-second execution limits, plain-TS copies compared with **Node v24.21.0**, `node --experimental-strip-types`. Final evidence: `probes-final.log`, `results.json`; runner: `python3 -I /tmp/pts-review-progs/run_probes.py`.
- Loop captures, basic async exception/finally/microtask ordering, Unicode, integer wrapping/saturation/division errors, `.par.map` ordering/exception joining, and Channel/Mutex/Once/shared-object handoff passed. Invalid `any`→number throwing is **intentional**, not a regression. Direct recursive accessors threw RangeError correctly; that suspected defect was rejected.
- CI: `gh run list --repo bcwongaa/parallelscript -L 10` showed newer builder runs, not this snapshot. Targeted `gh run view 38222328729 --repo bcwongaa/parallelscript --json headSha,status,conclusion` confirms **this exact SHA**, completed/success: [PTS run](https://github.com/bcwongaa/parallelscript/actions/runs/38222328729). Latest-main CI is not evidence about this detached snapshot.
- `git tag --list 'm[0-5]'`: all six tags present. `git diff --exit-code`: clean. `ldd` sampled binaries: libc/libm and loader, no JS engine or dynamic libuv/GC dependency.

## Scorecard

| Brief area / milestone | Status + evidence |
|---|---|
| §2.1 `.pts`, contextual keyword, Node compatibility | **Partial:** extension/identifier tests pass; confirmed plain-TS divergences below and extra prefix-operator restriction. |
| §2.2 pinned Go fork, marked changes, declarations | **Done infrastructure:** `docs/FORK.md:5–45`; dedicated `cmd/pts/`, `pts/`, prelude/modules; parser sample passes. |
| §2.3 typed IR, C11/native, source mapping | **Partial / diverged:** real typed IR and readable `#line` output; state machines constructed in emitter, not explicit post-pass IR (`pts/cgen/generators.go:33–97`). |
| §2.4 numbers and UTF-16 strings | **Done sampled:** probes `07`, `08`; numeric rules implemented, literal-before-`as` rounding/constant wrapping disclosed (`PROGRESS.md:162`). |
| §2.4 objects, unions, casts, checked `any`, generics | **Partial:** dynamic missing properties correctly return undefined; typed boundaries checked. Unchecked narrowing/initialization crashes and erased generic construction remain. |
| §2.5 GC/shared-heap safety | **Partial, not sign-off:** GC and buffer-capacity checks exist; four crashes and explicitly tolerated typed-array tearing contradict safety expectations. |
| §2.6 loops, pool, `.par`, `parallel for`, `pts:sync` | **Partial:** APIs/stress/scaling delivered; probe `09` preserves map order, `10` passes synchronization; ownership/rejection gaps remain (`PROGRESS.md:153`). |
| §2.6 single guardrail / no extra checks | **Diverged:** lexical assignment guardrail allows field mutation, but prefix function operands are additionally rejected (`pts/check/parallel.go:26–27`). |
| §2.7 modules/stdlib | **Done through M5 scope:** ES modules, fs/path/process, common builtins/JSON/promises/timers present; HTTP/fetch/RegExp belong to M6. |
| M0 bootstrap | **Done:** native build, CI/harness, tags; no hidden dependency on a JS runtime. |
| M1 core | **Partial semantics:** broad delivery; initialization/evaluation gaps. Nbody explicitly rescheduled to M2, then delivered. |
| M2 data/memory | **Partial:** GC/classes/interfaces, arrays/Map/Set, closures, enums, destructuring/spread/rest and binary-trees delivered; generic erasure departs from universal monomorphization and valid factories fail. |
| M3 TypeScript feel | **Partial:** unions/null/optional chains, exceptions/unknown/casts, checked dynamics, JSON/common methods/console delivered; 300-program corpus exists, **253 unskipped**, not 300 passing. |
| M4 async | **Partial / architecture diverged:** frames/loops/fs work; thenables, iterator completion, and suspension ordering fail. |
| M5 parallel | **Partial safety acceptance:** genuine APIs and speedups; cross-loop crash prevents unconditional completion claim. |

## Architecture, fork, and performance

The IR is genuinely typed/structured: boxing and closure captures are explicit (`pts/ir/ir.go:677–679,928–975`); specialized generic calls are represented rather than emitted directly from the TS AST. The principal architectural departure is generator/async frame-state construction inside C generation (`pts/cgen/generators.go:33–97`), contrary to brief §2.3’s already-transformed IR requirement. Moving it into an explicit pass would make effect/flow validation and later escape analysis easier; a rewrite is unnecessary. Syntactic loose-local inference (D-090) and silently incomplete effect classification (`pts/cgen/order.go:158`) are maintainability risks because syntax or a new node can alter semantic safety nonlocally.

Lowering spans 40 Go files/19,150 lines and emission 11/5,052 **including tests**; central files reach 3,588 and 1,725 lines. Favor independently validated pass boundaries over adding more state to these orchestrators.

Fork scope is encouraging: `git diff --stat typescript/v7.0.2 HEAD -- internal` reports **12 files, +403/−17**, concentrated in extension resolution, contextual parsing and numeric checker hooks, with a ledger (`docs/FORK.md`). Keep these hooks isolated and independently regression-tested.

Benchmark losses, warmups/repetitions, startup inclusion, shared-host noise and GC scaling limits are disclosed, so the tables are useful evidence—not universal speed claims. Word count really reads **64 files × 100k words** (`pts/bench/word_count/main.pts:7–9,53–54,70–73`); generated fixtures and repeated runs mean warmed filesystem-cache throughput, not cold-storage performance. **Spectral-norm is missing** from the benchmark inventory despite `docs/BRIEF.md:366`; add it to the plan or explicitly defer it. The sum/reduce Node twin fuses sum/max into one scan while PTS performs separate operations (`pts/bench/sum_reduce/main.ts:18–24`, `main.pts:18–20`): output parity is not equal work. The runner keeps means but deletes raw JSON/dispersion (`pts/bench/run.sh:20–21,88–100`); retain artifacts and interleave comparisons on this shared host. No timing rerun here; reported speedups were not independently reproduced.

## Findings — confirmed failures first

Probe names below refer to `/tmp/pts-review-progs/`. Reproduce with `/tmp/pts-review build FILE.pts -o /tmp/repro`, then `PTS_THREADS=2 /tmp/repro`; compare plain TS with Node. Native **SIGSEGV** means subprocess return `-11`, shell exit `139`; GDB independently confirmed all four crash paths.

1. **High — Clearing another loop’s timer crashes.** `11_foreign_timer.pts`:
   ```ts
   const timer = setTimeout(() => console.log("timer fired"), 1000);
   await parallel clearTimeout(timer);
   ```
   SIGSEGV at `pts/runtime/src/loop.c:243`: caller-local `state` is NULL. Timer handles have no owner (`pts/runtime/src/loop.c:14–25`); ref/refresh have the same affinity assumption. **Recommendation:** record owning loops and marshal operations to them, including pool-callback callers. Do not fix this by forbidding shared handles.

2. **High — Narrowed captured unions become invalid pointers after mutation.** `01_union_capture.pts`:
   ```ts
   function demo(): void {
       let value: string | number = "abc";
       const replace = (): void => { value = 123; };
       if (typeof value === "string") { replace(); console.log(value.length); }
   }
   demo();
   ```
   Native SIGSEGV; Node prints `undefined`. Identifier narrowing bypasses primitive checks (`pts/lower/unions.go:149–161`), then extracts an unchecked pointer (`pts/cgen/cgen.go:1046–1047`). **Recommendation:** check extraction from mutable captured/aliased storage, even when tsc retains narrowing.

3. **High — Captured-local TDZ is a crash path, not merely a compatibility gap.** `12_captured_tdz.pts` calls a hoisted closure before initializing its captured string:
   ```ts
   function demo(): void {
       function read(): void { console.log(value.length); }
       try { read(); } catch (error: any) { console.log(error.name); }
       const value = "ready";
   }
   demo();
   ```
   Native SIGSEGV; Node prints `ReferenceError`. Cell reads lack readiness checks (`pts/cgen/cgen.go:931–943`; acknowledged `PROGRESS.md:154`). **Recommendation:** readiness flags for early-captured cells. Also close documented class-TDZ/constructor-field NULL cases (`PROGRESS.md:155–156`); those were not independently reproduced.

4. **High — String coercion invokes an incompatible C return ABI.** `14_tostring_dynamic.pts`:
   ```ts
   class NumberString { toString(): any { return 7; } }
   const dynamic: any = new NumberString();
   dynamic.toString();
   console.log([new NumberString(), new NumberString()].join(","));
   ```
   Native SIGSEGV; Node prints `7,7`. `pts/runtime/src/array.c:580–583` assumes every named toString returns `pts_string *`, while dynamic descriptors include differently typed methods (`pts/cgen/classes.go:104`). GDB: `pts_string_concat` receives `0x401e000000000000` at `pts/runtime/src/string.c:215`. **Recommendation:** checked descriptor-driven OrdinaryToPrimitive invocation; no signature-blind function casts.

5. **High — Thenable resolution violates abrupt completion and callable capture.** `05_thenable_getters.pts` supplies a getter that increments a counter and returns a resolver closing over that count: native prints **`changing 2 2`**, Node **`changing 1 1`**. A getter throwing `Error("getter")` makes `Promise.resolve` throw synchronously instead of returning a rejected promise; resolving it inside `new Promise(resolve => resolve(bomb))` leaves that promise **pending**, versus Node’s **rejected** (30-ms race sentinel). Evidence: uncaught Get(then), `pts/runtime/src/promise.c:207,222–230`; consumed once flag, `:285–289`; queued invocation re-looks up the method, `:325–335`. **Recommendation:** catch getter failures and reject directly; invoke the captured callable with its original receiver exactly once.

6. **Medium — Suspension hoisting defers reads that JS already evaluated.** `06_yield_order.pts`: `return value + (yield 0)` with value initially 1, changed to 2 between `next()` calls, returns **12**, Node **11** after `next(10)`. A getter on the left is likewise called only after resumption. `pts/lower/yield_expressions.go:252–269` conflates effect-free with stable-across-suspension. **Recommendation:** snapshot values and assignment references before await/yield; cover compound assignments and getters. The observed generator bug also threatens shared await-hoisting logic.

7. **Medium — Iterator observation order and close precedence are wrong.** `13_iterator_protocol.pts` returns `{ get done(){return true}, get value(){throw Error("unneeded value")} }`: native reads value/throws; Node reads done only. A second iterator’s `return()` throws `"close"` after the body throws `"body"`: native catches **close**, Node **body**. Evidence: `pts/runtime/src/generator.c:169–170`; generic finally lowering, `pts/lower/generators.go:364`, `pts/lower/exceptions.go:118–120`. **Recommendation:** separate IteratorStep/IteratorValue and encode IteratorClose’s original-throw precedence, including async iteration. The fully annotated getter-result version also failed compilation (PTS1001); the reduced dynamic-result repro compiles.

8. **Medium — Valid erased generic factories reject their own results.** `03_generics_any.pts`: pass `make<T>(x:T): Box<T> { return new Box(x); }` into `exercise(factory: <T>(x:T)=>Box<T>)`, then call `factory(9)` as `Box<number>`. Native throws **`TypeError: Expected Box, got Box`**, Node prints **`box 9`**. Erasure constructs Box<any> and cannot convert it back (`PROGRESS.md:157`). **Recommendation:** dispatch applicable specialized generic construction/method instances, or explicitly constrain the supported contract; print type arguments in mismatch messages immediately.

9. **Medium — Additional parallel restrictions and reduction assumptions need ratification.** Probe `15`: `await parallel function (): number { return 42; }` is **PTS1002**; the call spelling `parallel(() => 42)` succeeds. Returning a function is legitimate arbitrary-expression task evaluation (brief §2.6), not a race requiring prohibition (`pts/check/parallel.go:26–27`). Probe `09`: range 0..3999 reduced by addition from 10 gives **7998100**, sequential **7998010**, because each leaf starts at initial (`pts/lib/parallel.pts:68–77`). The **identity** requirement is documented, but stronger than the brief’s associativity-only wording. **Recommendation:** remove prefix-only restrictions or obtain an explicit exception; prominently explain reduction identity/thread dependence.

10. **High — Race-safety guarantees remain unestablished beyond passing stress tests.** **Unverified runtime consequences:** typed-array source explicitly permits “torn” numbers (`pts/runtime/src/typed_array.c:4–5`), uses bulk memcpy/memmove, and inline shared accesses are non-atomic (`pts/runtime/include/pts.h:1526–1572`). Concurrent async-generator requests mutate an unsynchronized queue/frame (`pts/runtime/src/promise.c:940–948,1017–1029`); remote rejection/handler attachment is a documented fatal race (`PROGRESS.md:153`). No tearing, lost-request, or rejection-race repro was executed here. **Recommendation:** establish a C-level shared-memory contract and serialize generator ownership/handler checkpoints. x86 publication ordering is not blanket permission for C data races.

11. **Medium — Green infrastructure overstates semantic assurance.** All 47 corpus skips bypass execution, not just Node comparison (`pts/tests/run_test.go:194–200`), including known failures. Sanitizers are valuable, but TSan covers selected C tests with collection disabled (`pts/toolchain/runtime_test.go:98–107`), not generated race programs under GC. **Recommendation:** track compatibility passes, intentional differences, unsupported cases, crashes and flaky tests separately; preserve these new repros as regressions and add cross-loop-handle/lifecycle coverage.

## Decisions needing owner attention

Every REVIEW_LATER entry is classified; these are recommendations, not changes to locked decisions (`docs/agent/REVIEW_LATER.md:5–26`).

- **Agree:** D-005 fixed initial options; D-008/D-023/D-037 dependency scheduling; D-035/D-036 descriptor/site caches; D-048 numeric checker integration; D-068/D-069 exceptions/stacks; D-072 checked mutable views; D-074 argument boundaries; D-076/D-077 dynamic builtin/collection dispatch; D-089 corpus policy as an explicitly incomplete backlog; D-106 targeted specialization. Each preserves scope or supports typed safety.
- **Needs owner:** D-015/D-038 context-dependent indexing/dense-array exceptions; D-020 permitted interface merging; D-051 generic erasure; D-071 copy/drop-property conversions; D-075 adapter identity/bound-method semantics; D-090 whole-function syntactic undefined-test heuristics. These materially change ordinary TypeScript or locked architecture, even where SPEC §8 documents them.
- **Disagree with shipping D-105 as freeze:** `isFrozen` says true while writes silently succeed. This is an explicit documented difference, not an undiscovered bug; enforce immutability or expose an honestly named operation.
- **Additional needs owner:** D-109 emitter-level state machines versus `BRIEF.md:93–98`; D-078/D-079/D-101 erasure versus universal monomorphization; D-104 unchecked asserted union literals; D-120/D-123 restrictions above. D-111/D-121 recorded correctness gaps must be fixed, not ratified as memory-safety exceptions. D-130’s x86 target limitation and D-131’s measured GC tuning are reasonable scoped choices.

## Recommended master-plan changes: M6–M7

1. Insert **M6.0a Safety stabilization** before multicore HTTP acceptance: findings 1–4; captured/class initialization; owner-loop dispatch for runtime handles; generator ownership and a documented, tested shared-memory model. Gate on zero known accepted-program crash paths, not merely a green skipped suite.
2. Insert **M6.0b Compatibility stabilization**: findings 5–8, iterator abrupt completion, suspension snapshots and conservative/exhaustive IR effect handling (`pts/cgen/order.go:158` defaults unknown nodes to constants; broader ordering failures remain unverified). Classify all corpus skips and set a bounded defect budget.
3. Keep **M6.1d**, making every documentation sample executable in CI. Reconcile normative SPEC text with implementation notes; publish an identity/aliasing/indexing/freeze matrix and the reduce identity requirement.
4. Extend **M6.2/M6.3** acceptance to cross-loop handle use, disconnects/cancellation, backpressure, resource cleanup, pending requests and rejected promises. Throughput alone is insufficient.
5. Extend **M7.1/M7.2** to integer aliases, contextual-keyword ambiguities, prelude/import discovery and live PTS—not merely TS—diagnostics. Make **M7.3/M7.4** include clean-install smoke tests and resolution of the stabilization gates. Keep major GC optimization in M8.
6. Add **M6.5b Benchmark audit**: implement spectral-norm or explicitly defer it; align workloads, retain hyperfine JSON/dispersion, and distinguish warm-cache/startup-inclusive results from steady-state or cold-I/O claims.
