// The ParallelScript prelude: globals every .pts program sees, on top of lib.esnext.d.ts.
// Declarations appear here once the compiler supports them (docs/SPEC.md §7).

interface Console {
    log(...data: any[]): void;
}

declare const console: Console;

// Numeric types (docs/SPEC.md §3.2), declared as aliases of number. The checker gives
// each a number type of its own (D-048); the PTS semantic pass gives them their meaning.
// int is i64; f64 is number.
type int = number;
type i8 = number;
type i16 = number;
type i32 = number;
type i64 = int;
type u8 = number;
type u16 = number;
type u32 = number;
type u64 = number;
type f32 = number;
type f64 = number;
