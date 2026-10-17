// Sequential Node oracle for c07-red-serve-reuseport-handler-throws.pts: pts:http's serve is
// node:http's createServer, its handler given a fetch-standard Request made from the incoming
// message and its Response written back; a throw goes to the error handler if the server has
// one, else it is logged and answered 500, as pts:http does. AtomicInt is a counter, and
// `parallel client(base)` is `client(base)`.
import { createServer, type Server } from "node:http";

let errorCalls = 0;

// A handler that may throw: its request's own signal, then five aborted controllers.
function handle(request: Request): Response {
    if (request.signal.aborted) {
        return new Response("aborted already", { status: 500 });
    }
    for (let i = 0; i < 5; i++) {
        const controller = new AbortController();
        controller.abort(i);
    }
    return new Response("ok");
}

function serve(withErrorHandler: boolean): Promise<[Server, number]> {
    const server = createServer(async (message, out) => {
        let response: Response;
        try {
            response = handle(new Request(`http://127.0.0.1${message.url}`, { method: message.method }));
        } catch (error) {
            if (withErrorHandler) {
                errorCalls++;
                response = new Response(error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown", { status: 503 });
            } else {
                console.error(error);
                response = new Response("Internal Server Error", { status: 500 });
            }
        }
        out.writeHead(response.status, Object.fromEntries(response.headers));
        out.end(await response.text());
    });
    return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve([server, (server.address() as { port: number }).port])));
}

const handled = await serve(true);
const plain = await serve(false);

// 250 requests to base, 10 at a time; the bodies.
async function client(base: string): Promise<string[]> {
    const bodies: string[] = [];
    for (let batch = 0; batch < 25; batch++) {
        bodies.push(...(await Promise.all(Array.from({ length: 10 }, (_, i) => fetch(`${base}/${batch}/${i}`).then((r) => r.text())))));
    }
    return bodies;
}

// Each distinct body and how many times it came, sorted.
function tally(bodies: string[]): string {
    const counts = new Map<string, number>();
    for (const body of bodies) {
        counts.set(body, (counts.get(body) ?? 0) + 1);
    }
    return [...counts.keys()].sort().map((body) => `${body}: ${counts.get(body)}`).join(", ");
}

for (const [name, [, port]] of [["handled", handled], ["plain", plain]] as const) {
    const base = `http://127.0.0.1:${port}`;
    const bodies = (await Promise.all([0, 1, 2, 3].map(() => client(base)))).flat();
    console.log(`${name}: ${tally(bodies)}`);
}
for (const [server] of [handled, plain]) {
    server.closeAllConnections();
    server.close();
}
console.log(`error handler calls: ${errorCalls}`);
