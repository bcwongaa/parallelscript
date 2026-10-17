// Sequential Node oracle for c07-red-abortcontroller-parallel-tasks.pts: `parallel work(task)`
// is `Promise.resolve(work(task))`; everything else is unchanged.

// The work of one task: controllers made, listened to and aborted on the task's own loop.
function work(task: number): string {
    let failed = 0;
    let events = 0;
    let first = "";
    for (let i = 0; i < 20000; i++) {
        try {
            const controller = new AbortController();
            controller.signal.onabort = () => {
                events++;
            };
            controller.signal.addEventListener("abort", (event) => {
                if (event.type === "abort" && event.isTrusted) {
                    events++;
                }
            });
            controller.abort(task);
            if (!controller.signal.aborted || controller.signal.reason !== task) {
                throw new Error("wrong local signal");
            }
        } catch (error) {
            failed++;
            if (first === "") {
                first = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
            }
        }
    }
    return `task ${task}: failed ${failed}, events ${events}${first === "" ? "" : ` (first ${first})`}`;
}

const lines = await Promise.all([0, 1, 2, 3].map((task) => Promise.resolve(work(task))));
for (const line of lines) {
    console.log(line);
}
