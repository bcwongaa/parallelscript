// The ParallelScript prelude: globals every .pts program sees, on top of lib.esnext.d.ts.
// Declarations appear here once the compiler supports them (docs/SPEC.md §7).

interface Console {
    log(...data: any[]): void;
}

declare const console: Console;
