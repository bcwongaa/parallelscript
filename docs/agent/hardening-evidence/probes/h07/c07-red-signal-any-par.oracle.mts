// Sequential Node oracle for c07-red-signal-any-par.pts: `range(0, 4).par.map(f)` is
// `Array.from({ length: 4 }, (_, i) => f(i))`; everything else is unchanged.
const lines = Array.from({ length: 4 }, (_, task) => {
    let failed = 0;
    let first = "";
    for (let i = 0; i < 10000; i++) {
        try {
            const a = new AbortController();
            const b = new AbortController();
            const either = AbortSignal.any([a.signal, b.signal]);
            b.abort(i);
            if (!either.aborted || either.reason !== i || a.signal.aborted) {
                throw new Error("wrong any signal");
            }
        } catch (error) {
            failed++;
            if (first === "") {
                first = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
            }
        }
    }
    return `task ${task}: failed ${failed}${first === "" ? "" : ` (first ${first})`}`;
});
for (const line of lines) {
    console.log(line);
}
