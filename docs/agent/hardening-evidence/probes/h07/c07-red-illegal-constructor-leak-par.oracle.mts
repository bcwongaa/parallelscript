// Sequential Node oracle for c07-red-illegal-constructor-leak-par.pts: `range(0, 4).par.map(f)` is
// `Array.from({ length: 4 }, (_, i) => f(i))`; everything else is unchanged.

const lines = Array.from({ length: 4 }, (_, task) => {
    if (task === 0) {
        let illegal = 0;
        let constructed = 0;
        for (let i = 0; i < 20000; i++) {
            try {
                new AbortSignal();
                constructed++;
            } catch (error) {
                if (error instanceof TypeError && error.message === "Illegal constructor") {
                    illegal++;
                }
            }
        }
        return `task 0: illegal ${illegal}, constructed ${constructed}`;
    }
    let failed = 0;
    let first = "";
    for (let i = 0; i < 20000; i++) {
        try {
            const controller = new AbortController();
            controller.abort("local");
            if (!controller.signal.aborted || controller.signal.reason !== "local") {
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
