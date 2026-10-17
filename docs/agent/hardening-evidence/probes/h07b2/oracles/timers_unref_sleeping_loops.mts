// Unreferenced timers on loops that sleep (C120, H07b2): a loop with nothing referenced left
// sleeps, and its unreferenced timers must still fire at their time while some other loop
// keeps the program running, as Node's fire on its one loop. Here a task's unref'd
// setInterval ticks, and stops when another thread clears it; a task's timer that another
// thread unrefs still fires, and not before its delay has passed (a loop that slept counts
// it from when it was set, not from its stale time); a task's unref'd timer that another
// thread refreshes fires its whole delay after the refresh; a task's unref'd timer that
// another thread clears never fires, while one due after it on the same loop does; and the
// main loop's own unref'd timer fires while a task's referenced timer on another loop keeps
// the program running. Each wait has a 2 s deadline on the main loop, so a timer that never
// fires prints "never …"; the timing checks are lower bounds, which load can't break. The
// output is Node's for the program run sequentially (`parallel e` as `Promise.resolve(e)`).
// threads: 1 2 4

type Timer = ReturnType<typeof setTimeout>;

// p's value, or otherwise if p hasn't settled within ms (a referenced timer of this loop,
// which also keeps the main loop from taking the tasks it is waiting for).
function within<T>(ms: number, p: Promise<T>, otherwise: T): Promise<T> {
  return new Promise((resolve) => {
    const deadline = setTimeout(() => resolve(otherwise), ms);
    void p.then((value) => {
      clearTimeout(deadline);
      resolve(value);
    });
  });
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// Referenced until the last part: the main loop never runs out of work, so it doesn't take
// the tasks itself, and they run on other loops (at 2 or more threads).
const hold = setTimeout(() => {}, 10_000);

// An unref'd interval of a task: ticks while the program runs, stops when cleared.
const ticks = { count: 0 };
function startInterval(): { interval: Timer; ticked: Promise<string> } {
  let interval: Timer | undefined;
  const ticked = new Promise<string>((resolve) => {
    interval = setInterval(() => {
      ticks.count++;
      if (ticks.count === 5) {
        resolve("ticked 5 times");
      }
    }, 10);
  });
  interval!.unref();
  return { interval: interval!, ticked };
}
const ticking = await Promise.resolve(startInterval());
console.log("a task's unref'd interval:", await within(2_000, ticking.ticked, "never ticked 5 times"));
clearInterval(ticking.interval);
await sleep(30);
const afterClear = ticks.count;
await sleep(80);
console.log("cleared from another thread:", ticks.count === afterClear ? "stopped" : "still ticking");

// A task's referenced timer, unref'd from another thread: it still fires, not before its
// delay has passed (it counts from when it was set, not from when its loop last woke).
function referencedTimer(ms: number): { timer: Timer; fired: Promise<string> } {
  const setAt = performance.now();
  let timer: Timer | undefined;
  const fired = new Promise<string>((resolve) => {
    timer = setTimeout(() => {
      const early = ms - (performance.now() - setAt);
      resolve(early <= 5 ? "fired" : `fired ${Math.round(early)} ms early`);
    }, ms);
  });
  return { timer: timer!, fired };
}
const unreferenced = await Promise.resolve(referencedTimer(60));
unreferenced.timer.unref();
console.log("a task's timer unref'd from another thread:", await within(2_000, unreferenced.fired, "never fired"));

// A task's unref'd timer refreshed from another thread: it fires its delay after the refresh.
function stampedTimer(ms: number): { timer: Timer; firedAt: Promise<number> } {
  let timer: Timer | undefined;
  const firedAt = new Promise<number>((resolve) => {
    timer = setTimeout(() => resolve(performance.now()), ms);
  });
  timer!.unref();
  return { timer: timer!, firedAt };
}
const refreshed = await Promise.resolve(stampedTimer(300));
await sleep(40);
const refreshedAt = performance.now();
refreshed.timer.refresh();
const firedAt = await within(2_000, refreshed.firedAt, -1);
const sinceRefresh = firedAt - refreshedAt;
console.log(
  "a task's unref'd timer refreshed from another thread:",
  firedAt < 0 ? "never fired" : sinceRefresh >= 295 ? "fired its delay after the refresh" : `fired ${Math.round(sinceRefresh)} ms after the refresh`,
);

// Unref'd timers of tasks that another thread clears never fire; each task's later one does.
const cleared = { fired: 0 };
function clearedAndKept(): { doomed: Timer; kept: Promise<string> } {
  const doomed = setTimeout(() => cleared.fired++, 300);
  doomed.unref();
  const kept = new Promise<string>((resolve) => {
    setTimeout(() => resolve("fired"), 350).unref();
  });
  return { doomed, kept };
}
const pairs = await Promise.all([0, 1, 2, 3].map(() => Promise.resolve(clearedAndKept())));
for (const pair of pairs) {
  clearTimeout(pair.doomed);
}
const kept = await within(2_000, Promise.all(pairs.map((pair) => pair.kept)).then((all) => all.join(",")), "never fired");
console.log("tasks' later unref'd timers:", kept, "and those cleared from another thread:", cleared.fired === 0 ? "never fired" : `fired ${cleared.fired}`);

// The main loop's own unref'd timer while a task's referenced timer keeps the program running.
function work(ms: number, started: () => void): Promise<string> {
  started();
  return new Promise((resolve) => setTimeout(() => resolve("done"), ms));
}
let markStarted: () => void = () => {};
const workStarted = new Promise<void>((resolve) => {
  markStarted = () => resolve();
});
const working = Promise.resolve(work(300, () => markStarted()));
await workStarted;
clearTimeout(hold); // the task has started on another loop, and tasks never move once started
const mainTimer = { fired: false };
setTimeout(() => {
  mainTimer.fired = true;
}, 20).unref();
await working;
console.log("the main loop's unref'd timer while a task works:", mainTimer.fired ? "fired" : "never fired");

// expect:
// a task's unref'd interval: ticked 5 times
// cleared from another thread: stopped
// a task's timer unref'd from another thread: fired
// a task's unref'd timer refreshed from another thread: fired its delay after the refresh
// tasks' later unref'd timers: fired,fired,fired,fired and those cleared from another thread: never fired
// the main loop's unref'd timer while a task works: fired
