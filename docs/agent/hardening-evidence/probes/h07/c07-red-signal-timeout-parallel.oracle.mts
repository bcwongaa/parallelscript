// Sequential Node oracle for c07-red-signal-timeout-parallel.pts: `parallel e` is `Promise.resolve(e)`.

// label's 5,000 signals that would time out in an hour; their timers don't keep the program running.
function timeouts(label: string): string {
    let failed = 0;
    let first = "";
    for (let i = 0; i < 5000; i++) {
        try {
            const signal = AbortSignal.timeout(3_600_000);
            if (signal.aborted || signal.reason !== undefined) {
                throw new Error("a timeout signal aborted early");
            }
        } catch (error) {
            failed++;
            if (first === "") {
                first = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
            }
        }
    }
    return `${label}: failed ${failed}${first === "" ? "" : ` (first ${first})`}`;
}

const loops = await Promise.all([0, 1, 2, 3].map((task) => Promise.resolve(timeouts(`loop ${task}`))));
for (const line of loops) {
    console.log(line);
}
