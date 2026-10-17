// Sequential Node oracle for c07-shared-signal-fetch-par.pts: pts:http's serve is node:http's
// createServer (a held request's answer written when the program answers it), and
// `range(0, n).par.map(f)` is `Array.from({ length: n }, (_, i) => f(i))`.
import { createServer } from "node:http";

const COUNT = 400;
let held = 0;
let allHeld: () => void = () => {};
const waiting: ((text: string) => void)[] = [];
const server = createServer((_message, out) => {
    held++;
    if (held === COUNT) {
        allHeld();
    }
    waiting.push((text) => {
        if (!out.destroyed) {
            out.writeHead(200, { "content-type": "text/plain;charset=UTF-8" });
            out.end(text);
        }
    });
});
server.keepAliveTimeout = 0;
await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
const arrived = new Promise<void>((resolve) => {
    allHeld = () => resolve();
});

const shared = new AbortController();
const outcomes = Array.from({ length: COUNT }, (_, i) => {
    try {
        return fetch(`${base}/hold/${i}`, { signal: shared.signal }).then((r) => r.text(), (error) => (error instanceof Error ? (error.name === "AbortError" ? error.name : `${error.name}: ${error.message}`) : "non-Error"));
    } catch (error) {
        return Promise.resolve(error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown");
    }
});
// Wait until every request that started is held (or a second passes), then abort.
await Promise.race([arrived, new Promise((resolve) => setTimeout(resolve, 1000))]);
console.log(`held ${held}`);
shared.abort();
// Give the aborts a moment, then answer whatever the server still holds.
await new Promise((resolve) => setTimeout(resolve, 200));
for (const answer of waiting) {
    answer("late");
}
const settled = await Promise.race([Promise.all(outcomes), new Promise<string[]>((resolve) => setTimeout(() => resolve(["pending"]), 3000))]);
server.closeAllConnections();
server.close();
const counts = new Map<string, number>();
for (const outcome of settled) {
    counts.set(outcome, (counts.get(outcome) ?? 0) + 1);
}
console.log([...counts.keys()].sort().map((outcome) => `${outcome} ${counts.get(outcome)}`).join(", "));
