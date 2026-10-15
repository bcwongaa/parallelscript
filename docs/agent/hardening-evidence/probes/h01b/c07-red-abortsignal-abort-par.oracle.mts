// Sequential Node oracle for c07-red-abortsignal-abort-par.pts: `range(0, 4).par.map(f)` is
// `Array.from({ length: 4 }, (_, i) => f(i))`; everything else is unchanged.
const lines = Array.from({ length: 4 }, (_, task) => {
    let failed = 0;
    let first = "";
    for (let i = 0; i < 20000; i++) {
        try {
            const signal = AbortSignal.abort("local");
            if (!signal.aborted || signal.reason !== "local") {
                throw new Error("wrong local signal");
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
