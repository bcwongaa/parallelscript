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
    table(tabularData?: any, properties?: readonly string[]): void;
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

// Timers and microtasks as Node's (D-114): callbacks run on the event loop, each followed
// by the microtasks it queued. setTimeout and setInterval return a Timeout, which
// clearTimeout and clearInterval cancel (and ref/unref decide whether it keeps the
// program running); setImmediate's callbacks run after the loop polls.
interface Timeout {
    ref(): Timeout;
    unref(): Timeout;
    hasRef(): boolean;
    refresh(): Timeout;
}
interface Immediate {}
declare function setTimeout(callback: (...args: any[]) => void, ms?: number, ...args: any[]): Timeout;
declare function setInterval(callback: (...args: any[]) => void, ms?: number, ...args: any[]): Timeout;
declare function setImmediate(callback: (...args: any[]) => void, ...args: any[]): Immediate;
declare function clearTimeout(timeout: Timeout | string | number | undefined | null): void;
declare function clearInterval(timeout: Timeout | string | number | undefined | null): void;
declare function clearImmediate(immediate: Immediate | undefined | null): void;
declare function queueMicrotask(callback: () => void): void;

// performance.now(): milliseconds since the program started, with sub-millisecond precision.
interface Performance {
    now(): number;
}
declare const performance: Performance;

// The program's process as Node's global process: pts:process's default export (D-116).
declare var process: import("pts:process").Process;

// Node's global types that programs name: NodeJS.ErrnoException, the type of the errors of
// pts:fs and pts:process (D-117).
declare namespace NodeJS {
    type ErrnoException = import("pts:process").ErrnoException;
}

// `parallel e` (D-O05, D-120): the parser makes it a call of this function with e as the
// argument, and `parallel(e)` is one when no binding named parallel is in scope, so the
// checker types both as a promise of e's awaited value. The compiler runs e as a task.
declare function parallel<T>(task: T): Promise<Awaited<T>>;

// `xs.par`: a parallel view of an array (D-123), whose methods run on the fork-join pool.
interface Array<T> {
    readonly par: import("pts:parallel").ParallelArray<T>;
}
