// Sequential Node oracle for c07-control-events-par.pts: `range(0, 4).par.map(f)` is
// `Array.from({ length: 4 }, (_, i) => f(i))` and `parallel e` is `Promise.resolve(e)`.

// label's 5,000 rounds of local events, targets and exceptions.
function events(label: string): string {
    let failed = 0;
    let calls = 0;
    let canceled = 0;
    let codes = 0;
    let first = "";
    for (let i = 0; i < 5000; i++) {
        try {
            const target = new EventTarget();
            // Typed as an EventListener: a function of fewer parameters is wrapped in an adapter where one
            // goes, a different function for === (SPEC §8, D-041), so it would be added twice and never removed.
            const counted = (_event: Event): void => {
                calls++;
            };
            target.addEventListener("ping", counted);
            target.addEventListener("ping", counted); // added once
            target.addEventListener("ping", () => calls++, { once: true });
            target.addEventListener("ping", (event) => event.preventDefault());
            const ping = new Event("ping", { cancelable: true });
            if (!target.dispatchEvent(ping) && ping.defaultPrevented && ping.target === target) {
                canceled++;
            }
            target.dispatchEvent(new Event("ping")); // the once listener is gone
            target.removeEventListener("ping", counted);
            target.dispatchEvent(new Event("ping"));
            target.addEventListener("stop", (event) => {
                calls++;
                event.stopImmediatePropagation();
            });
            target.addEventListener("stop", () => {
                throw new Error("not reached");
            });
            target.dispatchEvent(new Event("stop"));
            const abort = new DOMException("why", "AbortError");
            const timeout = new DOMException("late", "TimeoutError");
            const custom = new DOMException("other", "Custom");
            if (!(abort instanceof Error) || abort.name !== "AbortError" || abort.message !== "why" || custom.code !== 0) {
                throw new Error("wrong DOMException");
            }
            codes += abort.code + timeout.code;
        } catch (error) {
            failed++;
            if (first === "") {
                first = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
            }
        }
    }
    return `${label}: failed ${failed}, calls ${calls}, canceled ${canceled}, codes ${codes}${first === "" ? "" : ` (first ${first})`}`;
}

const pool = Array.from({ length: 4 }, (_, task) => events(`pool ${task}`));
const loops = await Promise.all([0, 1, 2, 3].map((task) => Promise.resolve(events(`loop ${task}`))));
for (const line of [...pool, ...loops]) {
    console.log(line);
}
