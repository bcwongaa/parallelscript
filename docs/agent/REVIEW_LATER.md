# For the owner to review after DONE

Decisions made autonomously that are big enough to deserve a look. Nothing here blocks work.

- **D-005**: `pts` ignores `tsconfig.json`; every program is checked with fixed options (strict, ESNext, no DOM lib, every file a module). Revisit if projects need `include` lists or path mappings.
- **D-008**: the master plan lacked a task for imports between `.pts` files; M1.4 was split into M1.4a (control flow) and a new M1.4b (modules).
- **D-015**: reading a string index out of range gives `undefined` where the context can hold it (printing, concatenation, comparison with a string) and throws `RangeError` where a `string` is required (`const c: string = s[9]`), because a `string` can't hold `undefined`. A documented difference from Node (SPEC §8); arrays should follow the same rule in M2.4.
- **D-020**: "declaration merging" (brief §2.4) is rejected except for function overloads and interfaces merging only with interfaces, which change nothing at run time. The top-level code's C function is now `pts_main` (was `pts_fn_main`, D-007).
