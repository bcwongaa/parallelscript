# ParallelScript docs

Every document in the project, one line each. Paths are from the repo root.

## Start here

- [README.md](../README.md): the project's front page: what ParallelScript is, a quick start, a feature tour, benchmark highlights, status by milestone and the repo layout.
- [docs/GUIDE.md](GUIDE.md): a tour for TypeScript developers: what stays the same, what differs, the integer types, the parallel features and the guardrail, and the common compile errors with their fixes.

## Reference

- [docs/SPEC.md](SPEC.md): the language specification: syntax, toolchain, types and values, memory, concurrency, modules, the standard library, the documented differences from TypeScript and Node (§8) and implementation notes (§9). A behaviour that isn't in it isn't decided.
- [docs/ARCHITECTURE.md](ARCHITECTURE.md): how it's built: the pipeline (front end, PTS checks, IR, passes, C, clang), the runtime (values, GC, strings, event loops, the fork-join pool) and where each piece lives in the tree.
- [docs/DECISIONS.md](DECISIONS.md): the append-only decision log (D-Lnn locked by the owner, D-Onn decided by the orchestrator for the owner, D-nnn decided by agent sessions), each with its reason and the alternatives rejected; indexed by topic at the top.
- [docs/BENCHMARKS.md](BENCHMARKS.md): benchmark results per milestone against Node, Bun and Go, and parallel speedups over one thread, losses included, with how they were measured.
- [docs/FORK.md](FORK.md): the fork ledger: the typescript-go base tag, every modified upstream file (each edit marked `// PTS:`), how to merge a newer TypeScript release, and the upstream workflows disabled or limited.
- [docs/examples/](examples/): the samples the docs show, as `.pts` files with their expected output; they run as run tests (`TestRun`, and against Node for plain TypeScript).
- [pts/tests/README.md](../pts/tests/README.md): the test suites (run, error and stress tests, the 300-program corpus, the generated number and string corpora), the test directives and how the Node differential tests work.

## Plan and status

- [docs/MASTER_PLAN.md](MASTER_PLAN.md): the ordered task list, milestones M0 to M7, each task ticked with its commit hash, and the milestone checklist.
- [PROGRESS.md](../PROGRESS.md): the current state: milestone and next task, what's done, build and test commands with their baselines, known issues, needs from the owner and the session log.
- [docs/BRIEF.md](BRIEF.md): the owner's original mission brief: the pitch, the locked design decisions (§2), the tie-breakers (§3), testing, benchmarks and milestones. The source of truth behind the spec.
- [docs/UPSTREAM_README.md](UPSTREAM_README.md): TypeScript 7's README from typescript-go, kept as it was upstream.

## For the autonomous sessions

- [CLAUDE.md](../CLAUDE.md): the rules every session follows: reading order, owner overrides, working rules and the session protocol.
- [docs/agent/PREDECISIONS.md](agent/PREDECISIONS.md): the 17 decisions the orchestrator made on the owner's behalf before M0, copied into DECISIONS.md as D-O01 to D-O17.
- [docs/agent/REVIEW_LATER.md](agent/REVIEW_LATER.md): autonomous decisions big enough for the owner to review once the run is done; nothing in it blocks work.
- [docs/agent/NOTES-tsgo.md](agent/NOTES-tsgo.md): study notes on typescript-go's internals: where the scanner, parser, checker and program live, building a program, lib files and the prelude, file extensions, upstream tests.
- [docs/agent/NOTES-rayon.md](agent/NOTES-rayon.md): study notes on Rayon's design for the fork-join pool: the registry, `join`, the Chase–Lev deque, helping while waiting, adaptive splitting and the `.par` methods.
