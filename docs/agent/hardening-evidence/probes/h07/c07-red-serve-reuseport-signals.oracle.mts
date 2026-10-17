// Sequential Node oracle for c07-red-serve-reuseport-signals.pts: pts:http's serve is
// node:http's createServer, its handler given a fetch-standard Request made from the
// incoming message (as Bun's and pts:http's are); `parallel client(task)` is `client(task)`.
import { createServer } from "node:http";

const server = createServer((message, response) => {
    const request = new Request(`http://127.0.0.1:${port}${message.url}`, { method: message.method });
    let status = 200;
    let text: string;
    try {
        const own = request.signal;
        const controller = new AbortController();
        const forwarded = new Request(request.url, { signal: controller.signal });
        for (let step = 0; step < 8; step++) { // a controller per step of the handler's work, each aborted
            const local = new AbortController();
            local.abort(step);
            if (local.signal.reason !== step) {
                throw new Error("wrong local signal");
            }
        }
        controller.abort("done");
        if (own.aborted || !forwarded.signal.aborted || forwarded.signal.reason !== "done") {
            status = 500;
            text = "wrong signal state";
        } else {
            text = "ok";
        }
    } catch (error) {
        status = 500;
        text = error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown";
    }
    response.writeHead(status, { "content-type": "text/plain;charset=UTF-8" });
    response.end(text);
});
await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
const port = (server.address() as { port: number }).port;
const base = `http://127.0.0.1:${port}`;

// 500 requests, 10 at a time, without signals; the bodies.
async function client(task: number): Promise<string[]> {
    const bodies: string[] = [];
    for (let batch = 0; batch < 50; batch++) {
        const texts = await Promise.all(Array.from({ length: 10 }, (_, i) => fetch(`${base}/${task}/${batch}/${i}`).then((r) => r.text())));
        bodies.push(...texts);
    }
    return bodies;
}

const bodies = (await Promise.all([0, 1, 2, 3].map((task) => client(task)))).flat();
server.closeAllConnections();
server.close();
const counts = new Map<string, number>();
for (const body of bodies) {
    counts.set(body, (counts.get(body) ?? 0) + 1);
}
console.log([...counts.keys()].sort().map((body) => `${body}: ${counts.get(body)}`).join("\n"));
