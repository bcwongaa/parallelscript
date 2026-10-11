# For the owner to review after DONE

Decisions made autonomously that are big enough to deserve a look. Nothing here blocks work.

- **D-005**: `pts` ignores `tsconfig.json`; every program is checked with fixed options (strict, ESNext, no DOM lib, every file a module). Revisit if projects need `include` lists or path mappings.
- **D-008**: the master plan lacked a task for imports between `.pts` files; M1.4 was split into M1.4a (control flow) and a new M1.4b (modules).
- **D-015**: reading a string index out of range gives `undefined` where the context can hold it (printing, concatenation, comparison with a string) and throws `RangeError` where a `string` is required (`const c: string = s[9]`), because a `string` can't hold `undefined`. A documented difference from Node (SPEC §8); arrays should follow the same rule in M2.4.
- **D-023**: the brief lists nbody under M1, but its standard form needs arrays or classes (M2) and `Math.sqrt`; it moved to M2.7 instead of a throwaway scalar version. M1's benchmarks are fib (on `number`, against Node, Bun and Go) and fib on `int` (against Go).
- **D-020**: "declaration merging" (brief §2.4) is rejected except for function overloads and interfaces merging only with interfaces, which change nothing at run time. The top-level code's C function is now `pts_main` (was `pts_fn_main`, D-007).
- **D-035, D-036**: D-L10's "per-(concrete type, interface) tables built at compile time" became each descriptor's table of fields and of the methods and accessors interfaces may call, plus a one-word cache at every place that uses an interface (descriptor and offset or index). Values are one word (D-002), so a pair's table would have to be found from the object at each use anyway. Members must match the interface's IR types exactly (an `int` field doesn't provide a `number` property).
