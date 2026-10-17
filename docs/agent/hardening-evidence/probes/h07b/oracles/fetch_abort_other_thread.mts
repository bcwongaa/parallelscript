// fetch's signal aborted on another thread (C116, H07b), from a server that never answers, so a
// fetch that misses its abort never settles. First, one `.par` thread starts a fetch while
// another aborts its controller after a pause that varies from fetch to fetch, so the aborts
// land in each step of fetch's start: its Request following the signal, fetch's own
// throwIfAborted, exchange adding its abort listener. Then 200 fetches from `.par.map` share
// one controller's signal, which the main thread aborts once the server holds them all. Every
// fetch must reject with the reason, as in Node (the program run sequentially, `.par` as plain
// array methods); a fetch still pending 5 s after the others hung.
// threads: 1 2 4
import { createServer } from "node:http";
function range(start: number, end?: number): number[] & { toArray(): number[] } {
  if (end === undefined) {
    end = start;
    start = 0;
  }
  const items = Array.from({ length: end - start }, (_, i) => start + i) as number[] & { toArray(): number[] };
  items.toArray = () => [...items];
  return items;
}
class AtomicInt {
  #value: number;
  constructor(value = 0) {
    this.#value = value;
  }
  add(delta: number): number {
    const old = this.#value;
    this.#value += delta;
    return old;
  }
  load(): number {
    return this.#value;
  }
  store(value: number): void {
    this.#value = value;
  }
}

const FETCHES = 1_000;
const SHARING = 200;

let held = 0;
let allHeld: () => void = () => {};
const server = createServer((request) => {
  if (request.url === "/shared") {
    held++;
    if (held === SHARING) {
      allHeld();
    }
  }
  // never answers
});
await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", () => resolve()));
const port = (server.address() as { port: number }).port;
const base = `http://127.0.0.1:${port}`;

// Waits until count threads have arrived, so that they act at once; boundedly, since on one
// thread the callbacks run in turn.
function rendezvous(arrived: AtomicInt, count: number): void {
  arrived.add(1);
  for (let spin = 0; spin < 20_000 && arrived.load() < count; spin++) {}
}

function pause(cell: AtomicInt, spins: number): void {
  for (let spin = 0; spin < spins; spin++) {
    cell.load();
  }
}

function settle(fetching: Promise<Response>, outcomes: string[], i: number): Promise<void> {
  return fetching.then(
    () => {
      outcomes[i] = "answered";
    },
    (error: unknown) => {
      outcomes[i] = error === "stop" ? "rejected with the reason" : `rejected with ${String(error)}`;
    },
  );
}

// All of fetches settled, or 5 s passed.
async function settled(fetches: Promise<void>[]): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  await Promise.race([Promise.all(fetches), new Promise<void>((resolve) => {
    timer = setTimeout(resolve, 5_000);
  })]);
  clearTimeout(timer);
}

function tally(outcomes: string[]): string {
  const counts = new Map<string, number>();
  for (const outcome of outcomes) {
    counts.set(outcome, (counts.get(outcome) ?? 0) + 1);
  }
  return [...counts.keys()].sort().map((outcome) => `${outcome} ${counts.get(outcome)}`).join(", ");
}

{
  const outcomes = new Array<string>(FETCHES).fill("hung");
  const fetches: Promise<void>[] = [];
  for (let i = 0; i < FETCHES; i++) {
    const controller = new AbortController();
    const arrived = new AtomicInt(0);
    const spins = (i * 7919) % 8_000;
    range(0, 2).forEach((t) => {
      rendezvous(arrived, 2);
      if (t === 0) {
        fetches.push(settle(fetch(`${base}/alone`, { signal: controller.signal }), outcomes, i));
      } else {
        pause(arrived, spins);
        controller.abort("stop");
      }
    });
  }
  await settled(fetches);
  console.log("aborted on another thread as it starts:", tally(outcomes));
}

{
  const outcomes = new Array<string>(SHARING).fill("hung");
  const shared = new AbortController();
  const arrived = new Promise<void>((resolve) => {
    allHeld = () => resolve();
  });
  const fetches = range(0, SHARING).map((i) => settle(fetch(`${base}/shared`, { signal: shared.signal }), outcomes, i));
  let timer: ReturnType<typeof setTimeout> | undefined;
  await Promise.race([arrived, new Promise<void>((resolve) => {
    timer = setTimeout(resolve, 5_000);
  })]);
  clearTimeout(timer);
  shared.abort("stop");
  await settled(fetches);
  console.log("sharing one signal:", tally(outcomes));
}
server.closeAllConnections();
server.close();

// expect:
// aborted on another thread as it starts: rejected with the reason 1000
// sharing one signal: rejected with the reason 200
