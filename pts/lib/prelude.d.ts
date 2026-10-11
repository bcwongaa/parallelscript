// The ParallelScript prelude: globals every .pts program sees, on top of lib.esnext.d.ts.
// Declarations appear here once the compiler supports them (docs/SPEC.md §7).

interface Console {
    log(...data: any[]): void;
}

declare const console: Console;

// Numeric types (docs/SPEC.md §3.2). To tsc they are all number; the PTS semantic
// pass gives them their meaning. int is i64; f64 is number.
type int = number;
type i8 = number;
type i16 = number;
type i32 = number;
type i64 = number;
type u8 = number;
type u16 = number;
type u32 = number;
type u64 = number;
type f32 = number;
type f64 = number;
