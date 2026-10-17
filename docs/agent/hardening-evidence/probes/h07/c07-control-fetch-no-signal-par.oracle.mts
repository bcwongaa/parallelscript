// Sequential Node oracle for c07-control-fetch-no-signal-par.pts: pts:http's serve is node:http's
// createServer; `range(0, n).par.map(f)` is `Array.from({ length: n }, (_, i) => f(i))` and
// `parallel loopTask(task)` is `loopTask(task)`.
import { createServer } from "node:http";

const server = createServer((_message, response) => {
    response.writeHead(200, { "content-type": "text/plain;charset=UTF-8" });
    response.end("ok");
});
await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${(server.address() as { port: number }).port}`;

// The body of a fetch, or how it failed.
function outcome(response: Promise<Response>): Promise<string> {
    return response.then((r) => r.text(), (error) => (error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown"));
}

// Fetch i of a task without a signal, as a URL or a Request given none. A synchronous throw counts as a failure.
function fetchWithoutSignal(path: string, i: number): Promise<string> {
    try {
        return outcome(i % 2 === 0 ? fetch(`${base}${path}`) : fetch(new Request(`${base}${path}`)));
    } catch (error) {
        return Promise.resolve(error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown");
    }
}

// 200 fetches from a task on a loop, 20 at a time.
async function loopTask(task: number): Promise<string[]> {
    const texts: string[] = [];
    for (let batch = 0; batch < 10; batch++) {
        texts.push(...(await Promise.all(Array.from({ length: 20 }, (_, i) => fetchWithoutSignal(`/loop/${task}/${batch}`, i)))));
    }
    return texts;
}

const texts: string[] = [];
for (let round = 0; round < 4; round++) {
    texts.push(...(await Promise.all(Array.from({ length: 200 }, (_, i) => fetchWithoutSignal(`/pool/${round}`, i)))));
}
texts.push(...(await Promise.all([0, 1, 2, 3].map((task) => loopTask(task)))).flat());
server.closeAllConnections();
server.close();
const counts = new Map<string, number>();
for (const text of texts) {
    counts.set(text, (counts.get(text) ?? 0) + 1);
}
console.log([...counts.keys()].sort().map((text) => `${text}: ${counts.get(text)}`).join("\n"));
