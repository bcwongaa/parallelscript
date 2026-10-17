// Sequential Node twin of pts/tests/run/fetch_signals_parallel.pts (H07): pts:http's serve is
// node:http's createServer, its handler given a fetch-standard Request made from the incoming
// message; range(0, n).par.map(f) is Array.from({ length: n }, (_, i) => f(i)) and
// `parallel loopTask(task)` is `loopTask(task)`. The handler's code is the test's.
import { createServer } from "node:http";

function failure(error: unknown): string {
  return error instanceof Error ? `${error.name}: ${error.message}` : "non-Error thrown";
}

function handle(request: Request): Response {
  try {
    const own = request.signal; // made when first read
    const controller = new AbortController();
    const forwarded = new Request(request.url, { signal: controller.signal });
    const either = AbortSignal.any([controller.signal, AbortSignal.timeout(3_600_000)]);
    for (let step = 0; step < 8; step++) {
      const local = new AbortController();
      local.abort(step);
    }
    controller.abort("done");
    const fine = !own.aborted && forwarded.signal.reason === "done" && either.reason === "done";
    return new Response(fine ? "ok" : "server: wrong signal state", { status: fine ? 200 : 500 });
  } catch (error) {
    return new Response(`server: ${failure(error)}`, { status: 500 });
  }
}

const server = createServer(async (message, out) => {
  const response = handle(new Request(`http://127.0.0.1${message.url}`, { method: message.method }));
  out.writeHead(response.status, Object.fromEntries(response.headers));
  out.end(await response.text());
});
await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${(server.address() as { port: number }).port}`;

// Fetch i with a new signal of one of three kinds; on a pool thread AbortSignal.any's rather
// than AbortSignal.timeout's (a pool worker can't start timers yet).
function fetchWithSignal(path: string, i: number, onLoop: boolean): Promise<string> {
  try {
    let response: Promise<Response>;
    if (i % 3 === 0) {
      response = fetch(`${base}${path}`, { signal: new AbortController().signal });
    } else if (i % 3 === 1) {
      response = fetch(`${base}${path}`, { signal: onLoop ? AbortSignal.timeout(3_600_000) : AbortSignal.any([new AbortController().signal]) });
    } else {
      response = fetch(new Request(`${base}${path}`, { signal: new AbortController().signal }));
    }
    return response.then((r) => r.text(), (error) => `client: ${failure(error)}`);
  } catch (error) {
    return Promise.resolve(`client: ${failure(error)}`);
  }
}

// How many times each outcome occurs, outcomes sorted.
function tally(outcomes: string[]): string {
  const counts = new Map<string, number>();
  for (const outcome of outcomes) {
    counts.set(outcome, (counts.get(outcome) ?? 0) + 1);
  }
  return [...counts.keys()].sort().map((outcome) => `${outcome} ${counts.get(outcome)}`).join(", ");
}

// 250 fetches from a task on a loop, 25 at a time.
async function loopTask(task: number): Promise<string[]> {
  const outcomes: string[] = [];
  for (let batch = 0; batch < 10; batch++) {
    outcomes.push(...(await Promise.all(Array.from({ length: 25 }, (_, i) => fetchWithSignal(`/loop/${task}/${batch}`, i, true)))));
  }
  return outcomes;
}

const pooled: string[] = [];
for (let round = 0; round < 5; round++) {
  pooled.push(...(await Promise.all(Array.from({ length: 200 }, (_, i) => fetchWithSignal(`/pool/${round}`, i, false)))));
}
console.log("from .par.map:", tally(pooled));
const looped = await Promise.all([0, 1, 2, 3].map((task) => loopTask(task)));
console.log("from parallel tasks:", tally(looped.flat()));
server.closeAllConnections();
server.close();
