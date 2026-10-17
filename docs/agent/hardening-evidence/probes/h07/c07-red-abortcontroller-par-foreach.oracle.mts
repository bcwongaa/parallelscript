// Sequential Node oracle for c07-red-abortcontroller-par-foreach.pts: `range(0, n).par.forEach(f)`
// is `for (let i = 0; i < n; i++) f(i)`; everything else is unchanged.
const COUNT = 40_000;
const outcomes = new Array<string>(COUNT).fill("");

for (let i = 0; i < COUNT; i++) {
    try {
        const controller = new AbortController();
        controller.abort(i);
        outcomes[i] = controller.signal.aborted && controller.signal.reason === i ? "ok" : "wrong state";
    } catch (error) {
        outcomes[i] = error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown";
    }
}

const counts = new Map<string, number>();
for (const outcome of outcomes) {
    counts.set(outcome, (counts.get(outcome) ?? 0) + 1);
}
console.log([...counts.keys()].sort().map((outcome) => `${outcome}: ${counts.get(outcome)}`).join("\n"));
