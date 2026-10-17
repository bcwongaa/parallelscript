// Unreferenced timers never keep the program running (C120, H07b2): once every loop has only
// unreferenced timers left (tasks' long timeouts and short intervals, AbortSignal.timeout's,
// a task's timer that another thread unrefs, and the main loop's own), the program ends at
// once without firing the long ones, however many loops sleep. A task's unref'd timer that
// another thread refs keeps the program running until it fires. A program that waits for
// an unreferenced timer prints the timer's line (after 10 s) or hangs (2,000,000,000 ms).
// The output is Node's for the program run sequentially (`parallel e` as `Promise.resolve(e)`).
// threads: 1 2 4

const intervals = { ticks: 0 };

// Unreferenced timers on the task's loop: long ones that must never fire, and a short interval
// that may tick while the program runs.
function leaveUnreferenced(): string {
  setTimeout(() => console.log("a task's 10 s unref'd timer fired"), 10_000).unref();
  setTimeout(() => console.log("a task's very long unref'd timer fired"), 2_000_000_000).unref();
  setInterval(() => {
    intervals.ticks++;
  }, 5).unref();
  AbortSignal.timeout(10_000).addEventListener("abort", () => console.log("a task's 10 s timeout fired"));
  return "left";
}

// A task's referenced timer, for another thread to unref.
function longReferenced(): ReturnType<typeof setTimeout> {
  return setTimeout(() => console.log("a task's 10 s timer unref'd from another thread fired"), 10_000);
}

// A task's unref'd timer, for another thread to ref.
function shortUnreferenced(): ReturnType<typeof setTimeout> {
  const timer = setTimeout(() => console.log("a task's unref'd timer ref'd from another thread fired"), 100);
  timer.unref();
  return timer;
}

const hold = setTimeout(() => {}, 10_000); // referenced: the main loop doesn't take the tasks itself
const left = await Promise.all([0, 1, 2, 3, 4, 5, 6, 7].map(() => Promise.resolve(leaveUnreferenced())));
console.log("tasks", left.join(","));
const unreferenced = await Promise.resolve(longReferenced());
unreferenced.unref();
const referenced = await Promise.resolve(shortUnreferenced());
referenced.ref();
setTimeout(() => console.log("the main loop's 10 s unref'd timer fired"), 10_000).unref();
clearTimeout(hold);
console.log("the program's code is done");

// expect:
// tasks left,left,left,left,left,left,left,left
// the program's code is done
// a task's unref'd timer ref'd from another thread fired
