// The ParallelScript prelude: globals every .pts program sees, on top of lib.esnext.d.ts.
// Declarations appear here once the compiler supports them (docs/SPEC.md §7).

// console as Node's (D-088): log, info, debug and dirxml write to stdout, error and warn
// to stderr, each formatting its arguments as util.format does.
interface Console {
    log(...data: any[]): void;
    info(...data: any[]): void;
    debug(...data: any[]): void;
    dirxml(...data: any[]): void;
    error(...data: any[]): void;
    warn(...data: any[]): void;
    dir(item?: any, options?: ConsoleDirOptions): void;
    group(...label: any[]): void;
    groupCollapsed(...label: any[]): void;
    groupEnd(): void;
    assert(value?: any, ...data: any[]): void;
    trace(...data: any[]): void;
    count(label?: string): void;
    countReset(label?: string): void;
    time(label?: string): void;
    timeEnd(label?: string): void;
    timeLog(label?: string, ...data: any[]): void;
}

// The options of console.dir that ParallelScript supports: how deep to show objects
// (null for no limit) and whether to show non-enumerable properties.
interface ConsoleDirOptions {
    depth?: number | null;
    showHidden?: boolean;
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
