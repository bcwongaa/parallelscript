# For the owner to review after DONE

Decisions made autonomously that are big enough to deserve a look. Nothing here blocks work.

- **D-005**: `pts` ignores `tsconfig.json`; every program is checked with fixed options (strict, ESNext, no DOM lib, every file a module). Revisit if projects need `include` lists or path mappings.
- **D-008**: the master plan lacked a task for imports between `.pts` files; M1.4 was split into M1.4a (control flow) and a new M1.4b (modules).
